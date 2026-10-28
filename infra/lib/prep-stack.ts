import * as path from 'path';
import { CfnOutput, Duration, RemovalPolicy, SecretValue, Size, Stack, StackProps } from 'aws-cdk-lib';
import * as apigw from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpJwtAuthorizer } from 'aws-cdk-lib/aws-apigatewayv2-authorizers';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as cognito from 'aws-cdk-lib/aws-cognito';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import { Platform } from 'aws-cdk-lib/aws-ecr-assets';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as logs from 'aws-cdk-lib/aws-logs';
import { Construct } from 'constructs';

/** Where the site lives; the login returns there. */
export const SITE_ORIGIN = 'https://aleksbeloushkin-srb.github.io';
export const SITE_URL = `${SITE_ORIGIN}/golang-interview-prep/`;
export const LOCAL_ORIGIN = 'http://localhost:8000';
/** Prefix of the Cognito login domain; Google's client lists the same domain. */
export const AUTH_DOMAIN_PREFIX = 'golang-interview-prep';
/** Secrets Manager secret with {"clientId": …, "clientSecret": …} from Google. */
export const GOOGLE_SECRET = 'prep/google-oauth';

export interface PrepStackProps extends StackProps {
  /** Directory with the API Lambda's bootstrap binary (built by CI). */
  readonly apiCodeDir?: string;
}

export class PrepStack extends Stack {
  constructor(scope: Construct, id: string, props: PrepStackProps = {}) {
    super(scope, id, props);
    const repoRoot = path.join(__dirname, '..', '..');

    // --- Sign-in: Cognito user pool with Google as the only identity provider.
    // Who may sign in is decided in Google: the OAuth app is in "Testing" mode
    // with the owner as the only test user.
    const users = new cognito.UserPool(this, 'Users', {
      selfSignUpEnabled: false, // no passwords; federated users are still created on first sign-in
      signInAliases: { email: true },
      standardAttributes: { email: { required: true, mutable: true } },
      accountRecovery: cognito.AccountRecovery.NONE,
      featurePlan: cognito.FeaturePlan.ESSENTIALS,
      removalPolicy: RemovalPolicy.RETAIN,
    });
    const google = new cognito.UserPoolIdentityProviderGoogle(this, 'Google', {
      userPool: users,
      clientId: SecretValue.secretsManager(GOOGLE_SECRET, { jsonField: 'clientId' }).unsafeUnwrap(),
      clientSecretValue: SecretValue.secretsManager(GOOGLE_SECRET, { jsonField: 'clientSecret' }),
      scopes: ['openid', 'email', 'profile'],
      attributeMapping: {
        email: cognito.ProviderAttribute.GOOGLE_EMAIL,
        fullname: cognito.ProviderAttribute.GOOGLE_NAME,
      },
    });
    const domain = users.addDomain('Domain', { cognitoDomain: { domainPrefix: AUTH_DOMAIN_PREFIX } });
    const siteClient = users.addClient('Site', {
      generateSecret: false, // a browser app cannot keep a secret: Authorization Code + PKCE
      supportedIdentityProviders: [cognito.UserPoolClientIdentityProvider.GOOGLE],
      oAuth: {
        flows: { authorizationCodeGrant: true },
        scopes: [cognito.OAuthScope.OPENID, cognito.OAuthScope.EMAIL, cognito.OAuthScope.PROFILE],
        callbackUrls: [SITE_URL, `${LOCAL_ORIGIN}/`],
        logoutUrls: [SITE_URL, `${LOCAL_ORIGIN}/`],
      },
      accessTokenValidity: Duration.hours(1),
      idTokenValidity: Duration.hours(1),
      refreshTokenValidity: Duration.days(30),
      preventUserExistenceErrors: true,
    });
    siteClient.node.addDependency(google);

    // --- Data: one table, pk = USER#<sub>, sk = PROGRESS | ATTEMPT#…
    const table = new dynamodb.TableV2(this, 'Data', {
      partitionKey: { name: 'pk', type: dynamodb.AttributeType.STRING },
      sortKey: { name: 'sk', type: dynamodb.AttributeType.STRING },
      billing: dynamodb.Billing.onDemand(),
      removalPolicy: RemovalPolicy.RETAIN,
    });

    // --- API Lambda (Go, arm64): /me and /progress.
    const api = new lambda.Function(this, 'Api', {
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.ARM_64,
      handler: 'bootstrap',
      code: lambda.Code.fromAsset(props.apiCodeDir ?? path.join(repoRoot, 'dist', 'api')),
      memorySize: 256,
      timeout: Duration.seconds(10),
      environment: { TABLE_NAME: table.tableName },
      logGroup: new logs.LogGroup(this, 'ApiLogs', { retention: logs.RetentionDays.TWO_WEEKS, removalPolicy: RemovalPolicy.DESTROY }),
    });
    table.grantReadWriteData(api);

    // --- Runner Lambda: go test -race on submitted code. It runs in subnets
    // with no route to the internet and a security group with no rules, so the
    // code it runs cannot call out; its role only writes logs.
    const vpc = new ec2.Vpc(this, 'RunnerVpc', {
      availabilityZones: [`${this.region}a`, `${this.region}b`], // fixed, so synth needs no AWS lookup
      natGateways: 0,
      subnetConfiguration: [{ name: 'isolated', subnetType: ec2.SubnetType.PRIVATE_ISOLATED }],
    });
    const runnerSg = new ec2.SecurityGroup(this, 'RunnerSg', {
      vpc,
      allowAllOutbound: false,
      description: 'Runner: no inbound or outbound traffic',
    });
    const runner = new lambda.DockerImageFunction(this, 'Runner', {
      code: lambda.DockerImageCode.fromImageAsset(repoRoot, {
        file: 'runner.Dockerfile',
        platform: Platform.LINUX_AMD64,
        exclude: ['infra', 'dist', '.git', '.github', '**/node_modules', '**/cdk.out'],
      }),
      architecture: lambda.Architecture.X86_64,
      memorySize: 3008,
      ephemeralStorageSize: Size.gibibytes(2),
      timeout: Duration.seconds(29), // the HTTP API waits at most 30 s
      environment: { RUN_TIMEOUT: '25s' },
      vpc,
      vpcSubnets: { subnetType: ec2.SubnetType.PRIVATE_ISOLATED },
      securityGroups: [runnerSg],
      logGroup: new logs.LogGroup(this, 'RunnerLogs', { retention: logs.RetentionDays.TWO_WEEKS, removalPolicy: RemovalPolicy.DESTROY }),
    });

    // --- HTTP API: every route needs a Cognito access token.
    const httpApi = new apigw.HttpApi(this, 'HttpApi', {
      corsPreflight: {
        allowOrigins: [SITE_ORIGIN, LOCAL_ORIGIN],
        allowMethods: [apigw.CorsHttpMethod.GET, apigw.CorsHttpMethod.PUT, apigw.CorsHttpMethod.POST],
        allowHeaders: ['authorization', 'content-type'],
        maxAge: Duration.hours(1),
      },
    });
    // A low limit caps the cost if a token ever leaks.
    const stage = httpApi.defaultStage!.node.defaultChild as apigw.CfnStage;
    stage.defaultRouteSettings = { throttlingRateLimit: 5, throttlingBurstLimit: 10 };

    const authorizer = new HttpJwtAuthorizer('Cognito', `https://cognito-idp.${this.region}.amazonaws.com/${users.userPoolId}`, {
      jwtAudience: [siteClient.userPoolClientId], // access tokens carry client_id, which the authorizer checks
    });
    const apiIntegration = new HttpLambdaIntegration('ApiIntegration', api);
    httpApi.addRoutes({ path: '/me', methods: [apigw.HttpMethod.GET], integration: apiIntegration, authorizer });
    httpApi.addRoutes({ path: '/progress', methods: [apigw.HttpMethod.GET, apigw.HttpMethod.PUT], integration: apiIntegration, authorizer });
    httpApi.addRoutes({ path: '/runs', methods: [apigw.HttpMethod.POST], integration: new HttpLambdaIntegration('RunnerIntegration', runner), authorizer });

    // --- Values the site needs (public; they go into js/config.js).
    new CfnOutput(this, 'ApiUrl', { value: httpApi.apiEndpoint });
    new CfnOutput(this, 'AuthDomain', { value: domain.baseUrl() });
    new CfnOutput(this, 'UserPoolClientId', { value: siteClient.userPoolClientId });
    new CfnOutput(this, 'UserPoolId', { value: users.userPoolId });
  }
}
