import { App } from 'aws-cdk-lib';
import { PrepStack } from '../lib/prep-stack';

const app = new App();
new PrepStack(app, 'GolangInterviewPrep', {
  // The account is fixed: this app deploys only to the owner's account in Stockholm.
  env: { account: '851435021323', region: 'eu-north-1' },
});
