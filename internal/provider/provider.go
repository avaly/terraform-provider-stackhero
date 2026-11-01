// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"
	"os/exec"

	"github.com/avaly/terraform-provider-stackhero/internal/stackhero"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &StackheroProvider{}

type StackheroProvider struct {
	version string
}

type StackheroProviderModel struct {
	Token   types.String `tfsdk:"token"`
	CLIPath types.String `tfsdk:"cli_path"`
}

func (p *StackheroProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "stackhero"
	resp.Version = p.version
}

func (p *StackheroProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Stackhero services using the Stackhero CLI.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				MarkdownDescription: "Stackhero access token. Defaults to the `STACKHERO_TOKEN` environment variable.",
				Optional:            true,
				Sensitive:           true,
			},
			"cli_path": schema.StringAttribute{
				MarkdownDescription: "Path to the Stackhero CLI. Defaults to `stackhero` on `PATH`.",
				Optional:            true,
			},
		},
	}
}

func (p *StackheroProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data StackheroProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.Token.IsUnknown() || data.CLIPath.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Provider Configuration", "The Stackhero token and CLI path must be known before applying the configuration.")
		return
	}

	token := data.Token.ValueString()
	if data.Token.IsNull() {
		token = os.Getenv("STACKHERO_TOKEN")
	}
	if token == "" {
		resp.Diagnostics.AddError("Missing Stackhero Token", "Set the token provider attribute or the STACKHERO_TOKEN environment variable.")
		return
	}

	cliPath := data.CLIPath.ValueString()
	if data.CLIPath.IsNull() {
		cliPath = "stackhero"
	}
	resolvedPath, err := exec.LookPath(cliPath)
	if err != nil {
		resp.Diagnostics.AddError("Stackhero CLI Not Found", "Install the Stackhero CLI or configure cli_path with its executable path.")
		return
	}

	client := stackhero.NewClient(resolvedPath, token)
	if err := client.CheckVersion(ctx); err != nil {
		resp.Diagnostics.AddError("Unsupported Stackhero CLI", "Install Stackhero CLI version 1.2.0 or later. "+err.Error())
		return
	}
	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *StackheroProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewStackResource, NewRedisResource}
}

func (p *StackheroProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return nil
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &StackheroProvider{
			version: version,
		}
	}
}
