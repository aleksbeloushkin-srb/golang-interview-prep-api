import { test } from 'node:test';
import * as assert from 'node:assert/strict';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { AUTH_DOMAIN_PREFIX, GOOGLE_SECRET, PrepStack, RUNNER_CONCURRENCY, RUNS_PER_HOUR_ALARM, SITE_ORIGIN, SITE_URL } from '../lib/prep-stack';

// The API binary is built by CI; an empty placeholder is enough to synthesize.
const apiDir = fs.mkdtempSync(path.join(os.tmpdir(), 'api-'));
fs.writeFileSync(path.join(apiDir, 'bootstrap'), '');
const app = new App({ context: JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'cdk.context.json'), 'utf8')) });
const stack = new PrepStack(app, 'Test', { env: { account: '851435021323', region: 'eu-north-1' }, apiCodeDir: apiDir });
const t = Template.fromStack(stack);

test('Google sign-in reads both values from the secret', () => {
  t.hasResourceProperties('AWS::Cognito::UserPoolIdentityProvider', {
    ProviderName: 'Google',
    ProviderDetails: {
      client_id: `{{resolve:secretsmanager:${GOOGLE_SECRET}:SecretString:clientId::}}`,
      client_secret: `{{resolve:secretsmanager:${GOOGLE_SECRET}:SecretString:clientSecret::}}`,
    },
  });
  t.hasResourceProperties('AWS::Cognito::UserPoolDomain', { Domain: AUTH_DOMAIN_PREFIX });
});

test('the site client uses the code flow, no secret, and returns to the site', () => {
  t.hasResourceProperties('AWS::Cognito::UserPoolClient', {
    GenerateSecret: false,
    AllowedOAuthFlows: ['code'],
    SupportedIdentityProviders: ['Google'],
    CallbackURLs: Match.arrayWith([SITE_URL]),
  });
  t.hasResourceProperties('AWS::Cognito::UserPool', {
    AdminCreateUserConfig: { AllowAdminCreateUserOnly: true },
  });
});

test('every route requires the JWT authorizer', () => {
  const routes = t.findResources('AWS::ApiGatewayV2::Route');
  assert.equal(Object.keys(routes).length, 4);
  for (const [id, r] of Object.entries(routes)) {
    assert.equal((r as any).Properties.AuthorizationType, 'JWT', id);
  }
  t.hasResourceProperties('AWS::ApiGatewayV2::Api', {
    CorsConfiguration: Match.objectLike({ AllowOrigins: Match.arrayWith([SITE_ORIGIN]) }),
  });
  t.hasResourceProperties('AWS::ApiGatewayV2::Stage', {
    DefaultRouteSettings: { ThrottlingRateLimit: 5, ThrottlingBurstLimit: 10 },
  });
});

test('the runner has no way out: isolated subnets, no NAT, no egress rules', () => {
  t.resourceCountIs('AWS::EC2::NatGateway', 0);
  t.resourceCountIs('AWS::EC2::InternetGateway', 0);
  t.hasResourceProperties('AWS::Lambda::Function', {
    PackageType: 'Image',
    VpcConfig: Match.objectLike({ SubnetIds: Match.anyValue() }),
    Timeout: 29,
  });
});

test('data is kept when the stack is deleted', () => {
  t.hasResource('AWS::DynamoDB::GlobalTable', { DeletionPolicy: 'Retain' });
  t.hasResource('AWS::Cognito::UserPool', { DeletionPolicy: 'Retain' });
});

test('the runner is capped: reserved concurrency and a tighter limit on /runs', () => {
  t.hasResourceProperties('AWS::Lambda::Function', { PackageType: 'Image', ReservedConcurrentExecutions: RUNNER_CONCURRENCY });
  t.hasResourceProperties('AWS::ApiGatewayV2::Stage', {
    RouteSettings: { 'POST /runs': { ThrottlingRateLimit: 1, ThrottlingBurstLimit: 2 } },
  });
});

test('an alarm watches the runner; email only when the deploy passes one', () => {
  t.hasResourceProperties('AWS::CloudWatch::Alarm', {
    MetricName: 'Invocations', Namespace: 'AWS/Lambda', Statistic: 'Sum', Period: 3600,
    Threshold: RUNS_PER_HOUR_ALARM, ComparisonOperator: 'GreaterThanThreshold', TreatMissingData: 'notBreaching',
    AlarmActions: Match.anyValue(),
  });
  t.resourceCountIs('AWS::SNS::Subscription', 0);
  const withEmail = new PrepStack(new App({ context: JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'cdk.context.json'), 'utf8')) }), 'WithEmail',
    { env: { account: '851435021323', region: 'eu-north-1' }, apiCodeDir: apiDir, alertEmail: 'alerts@example.com' });
  Template.fromStack(withEmail).hasResourceProperties('AWS::SNS::Subscription', { Protocol: 'email', Endpoint: 'alerts@example.com' });
});

