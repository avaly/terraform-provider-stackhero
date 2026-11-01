package provider

import (
	"context"
	"os"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderMetadataAndSchema(t *testing.T) {
	provider := New("test")()
	var metadata frameworkprovider.MetadataResponse
	provider.Metadata(context.Background(), frameworkprovider.MetadataRequest{}, &metadata)
	if metadata.TypeName != "stackhero" || metadata.Version != "test" {
		t.Fatalf("unexpected provider metadata: %+v", metadata)
	}
	var response frameworkprovider.SchemaResponse
	provider.Schema(context.Background(), frameworkprovider.SchemaRequest{}, &response)
	token, ok := response.Schema.Attributes["token"].(schema.StringAttribute)
	if !ok || !token.Optional || !token.Sensitive {
		t.Fatal("token must be optional and sensitive")
	}
	if len(provider.Resources(context.Background())) != 2 {
		t.Fatal("expected stack and Redis resources to be registered")
	}
}

func TestProviderConfigure(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKHERO_PROVIDER_FAKE_CLI", "1")
	t.Setenv("STACKHERO_PROVIDER_FAKE_STATE", t.TempDir()+"/state.json")
	for _, test := range []struct {
		name    string
		token   any
		env     string
		cliPath any
		wantErr bool
	}{
		{name: "environment token", env: "test-token", cliPath: path},
		{name: "configured token", token: "test-token", cliPath: path},
		{name: "missing token", cliPath: path, wantErr: true},
		{name: "missing executable", env: "test-token", cliPath: "/does/not/exist", wantErr: true},
		{name: "unknown token", token: tftypes.UnknownValue, cliPath: path, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("STACKHERO_TOKEN", test.env)
			ctx := context.Background()
			provider := New("test")()
			var schemaResp frameworkprovider.SchemaResponse
			provider.Schema(ctx, frameworkprovider.SchemaRequest{}, &schemaResp)
			objectType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"token": tftypes.String, "cli_path": tftypes.String}}
			config := tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objectType, map[string]tftypes.Value{
				"token":    tftypes.NewValue(tftypes.String, test.token),
				"cli_path": tftypes.NewValue(tftypes.String, test.cliPath),
			})}
			var response frameworkprovider.ConfigureResponse
			provider.Configure(ctx, frameworkprovider.ConfigureRequest{Config: config}, &response)
			if response.Diagnostics.HasError() != test.wantErr {
				t.Fatalf("unexpected configure diagnostics: %v", response.Diagnostics)
			}
			if !test.wantErr && response.ResourceData == nil {
				t.Fatal("expected a configured CLI client")
			}
		})
	}
}
