# Terraform Provider Stackhero

It manages [Stackhero.io](https://www.stackhero.io/) stacks and Redis services.

The provider uses the [Stackhero CLI](https://www.stackhero.io/en-US/stackhero/documentations/Use-the-CLI) on the machine running Terraform. The CLI may update itself; changes to its output can affect the provider.

## Requirements

- Terraform 1.0 or later
- Go 1.25.8 or later to build
- Stackhero CLI 1.2.0 or later installed on the Terraform execution host
- A Stackhero access token

## Configuration

Create an access token in the Stackhero dashboard and provide it through `STACKHERO_TOKEN`. Alternatively, set the sensitive `token` provider attribute. Set `cli_path` when the executable is not on `PATH`.

```terraform
terraform {
  required_providers {
    stackhero = {
      source  = "avaly/stackhero"
      version = "~> 0.1.0"
    }
  }
}
provider "stackhero" {
	cli_path = "stackhero"
}
```

## Redis

Create a `stackhero_stack`, or supply an existing stack ID to `stackhero_redis`. Destroying Redis does not delete its stack; a stack must be empty before it can be deleted. Redis deletion waits for the service to disappear from CLI listings.

The Redis resource supports `configuration_json` for `tlsOnly`, `saveToDisk`, `maxMemoryPolicy`, `modules`, and `notifyKeyspaceEvents`. Settings are merged with the full remote configuration. Removing a previously managed setting requires resource replacement. Configuration updates can restart Redis and can overwrite concurrent external configuration changes.

The CLI passes the full configuration, including credentials, in process arguments. Other local processes may be able to read them. Redis passwords and managed settings are stored in Terraform state even when marked sensitive; protect state and plan files accordingly.

The CLI does not expose a service's instance size or region in its read responses. The provider cannot detect drift in those fields or safely import Redis services. Stack import is supported by stack ID, but the CLI cannot recover the organization from a stack listing. Adding `organization` after import plans replacement, so review the plan carefully. If creation fails after the service is created, use the service ID in the diagnostic to inspect and manually delete it before retrying.

## Development

```shell
go test ./...
go build ./...
make generate
```

Live acceptance tests create billable resources and must be run explicitly with a dedicated account. They create a stack and Redis service, update its settings, and destroy both. Failures can leave resources behind; check the dashboard after a failed run. Never expose access tokens or generated credentials in logs or test fixtures.

```shell
TF_ACC=1 STACKHERO_LIVE_TEST=1 go test -run '^TestLiveRedisLifecycle$' -timeout 20m ./internal/provider
```

Set `STACKHERO_TEST_INSTANCE` or `STACKHERO_TEST_REGION` to override the `1G` and `europe` defaults. The opt-in `TestTerraformLifecycleWithFakeCLI` test uses a fake CLI and does not create resources.
