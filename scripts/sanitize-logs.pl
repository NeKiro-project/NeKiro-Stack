use strict;
use warnings;

my @secret_names = qw(
  POSTGRES_PASSWORD NEKIRO_COMPOSE_DATABASE_URL
  NEKIRO_ROUTER_INTERNAL_BEARER_TOKEN NEKIRO_CONTROL_PLANE_SERVICE_TOKEN
  NEKIRO_ROUTER_AGENT_CREDENTIAL_KEY_ID NEKIRO_ROUTER_AGENT_CREDENTIAL_PRIVATE_KEY_BASE64URL
  NEKIRO_AGENT_ROUTER_KEY_ID NEKIRO_AGENT_ROUTER_PUBLIC_KEY_BASE64URL
  RUNTIME_A_ROUTER_TOKEN RUNTIME_B_ROUTER_TOKEN NEKIRO_E2E_ROUTER_TOKEN
  NEKIRO_E2E_OWNER_TOKEN NEKIRO_E2E_USER_TOKEN NEKIRO_E2E_OTHER_TOKEN
  NEKIRO_E2E_DATABASE_URL VITE_NEKIRO_PROVIDER_TOKEN VITE_NEKIRO_OWNER_TOKEN
);
my @secrets = grep { defined($_) && length($_) } @ENV{@secret_names};
my @fixtures = qw(
  direct-json-value direct-sse-value nested-value browser-json browser-sse
  policy-content-secret protocol-content-secret agent-content-secret
  route-content-secret timeout-content-secret cancel-content-secret
  interrupted-content-secret dependency-content-secret dependency-raw-secret
  direct-agent-must-not-execute credential-forged-must-not-execute
  credential-expired-must-not-execute credential-wrong-audience-must-not-execute
  disabled-installation-must-not-execute suspended-release-must-not-execute
  revoked-release-must-not-execute
);

while (my $line = <STDIN>) {
  for my $value (@secrets, @fixtures) {
    $line =~ s/\Q$value\E/[REDACTED]/g;
  }
  $line =~ s/\b[0-9a-f]{64}\b/[REDACTED-64-HEX]/g;
  $line =~ s{(?:[A-Za-z0-9_-]+\.){2}[A-Za-z0-9_-]+}{[REDACTED-ROUTER-CREDENTIAL]}g;
  $line =~ s/\b[A-Za-z0-9_-]{86}\b/[REDACTED-ED25519-SIGNATURE]/g;
  $line =~ s/\brtj_[A-Za-z0-9._:-]*/[REDACTED-ROUTER-JTI]/g;
  $line =~ s/\bconcurrent-[A-Za-z0-9._:-]*/[REDACTED-FIXTURE]/g;
  print $line;
}
