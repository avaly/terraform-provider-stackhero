package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/avaly/terraform-provider-stackhero/internal/stackhero"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &StackResource{}
var _ resource.ResourceWithImportState = &StackResource{}

type stackCLI interface {
	JSON(context.Context, string, ...string) (json.RawMessage, error)
	Script(context.Context, string, ...string) (string, error)
}

type StackResource struct {
	client stackCLI
}

type StackResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Organization types.String `tfsdk:"organization"`
}

func NewStackResource() resource.Resource {
	return &StackResource{}
}

func (stack *StackResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_stack"
}

func (stack *StackResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Stackhero stack. The stack must be empty before deletion.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Stackhero stack ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Stack name. Changing this value requires replacement.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"organization": schema.StringAttribute{
				MarkdownDescription: "Organization name or ID. Omit when the account has one organization. Changing this value requires replacement.",
				Optional:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (stack *StackResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*stackhero.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Provider Configuration", fmt.Sprintf("Expected a Stackhero CLI client, got %T.", req.ProviderData))
		return
	}
	stack.client = client
}

func (stack *StackResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data StackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if strings.TrimSpace(data.Name.ValueString()) == "" {
		resp.Diagnostics.AddError("Invalid Stack Name", "The stack name must not be empty.")
		return
	}
	args := []string{"--name=" + data.Name.ValueString()}
	if !data.Organization.IsNull() {
		args = append(args, "--organization="+data.Organization.ValueString())
	}
	id, err := stack.client.Script(ctx, "stack-create", args...)
	if err != nil {
		resp.Diagnostics.AddError("Stack Creation Failed", "The create request may have succeeded. Check the Stackhero dashboard or stack list before retrying or importing the stack. "+err.Error())
		return
	}
	if !strings.HasPrefix(id, "stk-") || len(id) == len("stk-") {
		resp.Diagnostics.AddError("Stack Creation Failed", "The CLI returned an invalid stack ID. Check the Stackhero dashboard or stack list before retrying or importing the stack.")
		return
	}
	data.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (stack *StackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data StackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name, found, err := stackName(ctx, stack.client, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Stack Refresh Failed", err.Error())
		return
	}
	if !found {
		resp.Diagnostics.AddError("Stack Not Found", "The stack was not returned by the Stackhero CLI. State was preserved because list scope and consistency cannot yet prove deletion.")
		return
	}
	data.Name = types.StringValue(name)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func stackName(ctx context.Context, client stackCLI, id string) (string, bool, error) {
	output, err := client.JSON(ctx, "stacks-list")
	if err != nil {
		return "", false, err
	}
	var response struct {
		Success *bool `json:"success"`
		Data    *[]struct {
			ID   *string `json:"id"`
			Name *string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &response); err != nil || response.Success == nil || !*response.Success || response.Data == nil {
		return "", false, fmt.Errorf("stackhero stacks-list returned an unexpected response")
	}
	for _, entry := range *response.Data {
		if entry.ID == nil || entry.Name == nil || *entry.ID == "" {
			return "", false, fmt.Errorf("stackhero stacks-list returned incomplete stack data")
		}
		if *entry.ID == id {
			return *entry.Name, true, nil
		}
	}
	return "", false, nil
}

func (stack *StackResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unsupported Stack Update", "Stack name and organization changes require replacement.")
}

func (stack *StackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data StackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := stack.client.JSON(ctx, "stack-delete", "--stack="+data.ID.ValueString(), "--confirm")
	if err != nil && !errors.Is(err, stackhero.ErrNotFound) {
		resp.Diagnostics.AddError("Stack Deletion Failed", "The stack must be empty before deletion. State was preserved. "+err.Error())
	}
}

func (stack *StackResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
