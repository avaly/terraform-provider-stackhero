package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/avaly/terraform-provider-stackhero/internal/stackhero"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &RedisResource{}

type RedisResource struct {
	client stackCLI
}

type RedisResourceModel struct {
	ID            types.String `tfsdk:"id"`
	StackID       types.String `tfsdk:"stack_id"`
	Instance      types.String `tfsdk:"instance"`
	Region        types.String `tfsdk:"region"`
	Organization  types.String `tfsdk:"organization"`
	Domain        types.String `tfsdk:"domain"`
	Password      types.String `tfsdk:"password"`
	Configuration types.String `tfsdk:"configuration_json"`
}

func NewRedisResource() resource.Resource {
	return &RedisResource{}
}

func (redis *RedisResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_redis"
}

func (redis *RedisResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Stackhero Redis service. The CLI does not expose instance size or region on read, so drift in those fields cannot be detected.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Stackhero service ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"stack_id":     schema.StringAttribute{MarkdownDescription: "ID of the stack that contains Redis.", Required: true, PlanModifiers: replace},
			"instance":     schema.StringAttribute{MarkdownDescription: "Redis instance size, such as `1G`.", Required: true, PlanModifiers: replace},
			"region":       schema.StringAttribute{MarkdownDescription: "Region identifier, such as `europe`.", Required: true, PlanModifiers: replace},
			"organization": schema.StringAttribute{MarkdownDescription: "Organization name or ID, when needed to resolve the Redis service store.", Optional: true, PlanModifiers: replace},
			"domain":       schema.StringAttribute{MarkdownDescription: "Redis host name.", Computed: true},
			"password":     schema.StringAttribute{MarkdownDescription: "Generated Redis password. Stored in Terraform state.", Computed: true, Sensitive: true},
			"configuration_json": schema.StringAttribute{
				MarkdownDescription: "JSON object of managed Redis settings. Removing a managed key requires replacement. Values are sensitive and passed to the CLI in process arguments.",
				Optional:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplaceIf(redisConfigurationRequiresReplace, "Removing a managed setting requires replacement.", "Removing a managed setting requires replacement.")},
			},
		},
	}
}

func (redis *RedisResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*stackhero.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Provider Configuration", fmt.Sprintf("Expected a Stackhero CLI client, got %T.", req.ProviderData))
		return
	}
	redis.client = client
}

func (redis *RedisResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data RedisResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if strings.TrimSpace(data.StackID.ValueString()) == "" || strings.TrimSpace(data.Instance.ValueString()) == "" || strings.TrimSpace(data.Region.ValueString()) == "" {
		resp.Diagnostics.AddError("Invalid Redis Configuration", "Stack ID, instance and region must not be empty.")
		return
	}
	if !data.Configuration.IsNull() {
		if _, err := parseRedisSettings(data.Configuration.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid Redis Configuration", err.Error())
			return
		}
	}
	args := []string{"--stack=" + data.StackID.ValueString(), "--service-store=redis", "--instance=" + data.Instance.ValueString(), "--region=" + data.Region.ValueString()}
	if !data.Organization.IsNull() {
		args = append(args, "--organization="+data.Organization.ValueString())
	}
	id, err := redis.client.Script(ctx, "service-add", args...)
	if err != nil {
		resp.Diagnostics.AddError("Redis Creation Failed", "The create request may have succeeded. Check the Stackhero dashboard or service list before retrying. "+err.Error())
		return
	}
	if !strings.HasPrefix(id, "svc-") || len(id) == len("svc-") {
		resp.Diagnostics.AddError("Redis Creation Failed", "The CLI returned an invalid service ID. Check the Stackhero dashboard or service list before retrying.")
		return
	}
	data.ID = types.StringValue(id)
	data.Domain = types.StringNull()
	data.Password = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := waitForRedis(ctx, redis.client, id, "running"); err != nil {
		resp.Diagnostics.AddError("Redis Creation Incomplete", "Service "+id+" was created but did not become ready. Check it in Stackhero and delete it manually if necessary. "+err.Error())
		return
	}
	if !data.Configuration.IsNull() {
		if err := applyRedisSettings(ctx, redis.client, id, data.Configuration.ValueString()); err != nil {
			resp.Diagnostics.AddError("Redis Creation Incomplete", "Service "+id+" was created but configuration failed. Check it in Stackhero and delete it manually if necessary. "+err.Error())
			return
		}
	}
	domain, password, err := redisConnection(ctx, redis.client, id)
	if err != nil {
		resp.Diagnostics.AddError("Redis Creation Incomplete", "Service "+id+" was created but its connection details could not be read. Check it in Stackhero and delete it manually if necessary. "+err.Error())
		return
	}
	data.Domain = types.StringValue(domain)
	data.Password = types.StringValue(password)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (redis *RedisResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data RedisResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	stackID, found, err := redisStackID(ctx, redis.client, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Redis Refresh Failed", err.Error())
		return
	}
	domain, password, err := redisConnection(ctx, redis.client, data.ID.ValueString())
	if !found && errors.Is(err, stackhero.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil || !found {
		resp.Diagnostics.AddError("Redis Refresh Failed", "The service could not be reliably read. State was preserved.")
		return
	}
	data.StackID = types.StringValue(stackID)
	data.Domain = types.StringValue(domain)
	data.Password = types.StringValue(password)
	if !data.Configuration.IsNull() {
		settings, err := readManagedRedisSettings(ctx, redis.client, data.ID.ValueString(), data.Configuration.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Redis Refresh Failed", "Unable to read managed settings. State was preserved. "+err.Error())
			return
		}
		data.Configuration = types.StringValue(settings)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (redis *RedisResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data RedisResourceModel
	var previous RedisResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.Configuration.IsNull() {
		resp.Diagnostics.AddError("Unsupported Redis Update", "Removing managed settings requires replacement.")
		return
	}
	if !previous.Configuration.IsNull() {
		previousSettings, previousErr := parseRedisSettings(previous.Configuration.ValueString())
		nextSettings, nextErr := parseRedisSettings(data.Configuration.ValueString())
		if previousErr != nil || nextErr != nil {
			resp.Diagnostics.AddError("Invalid Redis Configuration", "Managed settings must be valid JSON objects.")
			return
		}
		for key := range previousSettings {
			if _, ok := nextSettings[key]; !ok {
				resp.Diagnostics.AddError("Unsupported Redis Update", "Removing managed settings requires replacement.")
				return
			}
		}
	}
	if err := applyRedisSettings(ctx, redis.client, data.ID.ValueString(), data.Configuration.ValueString()); err != nil {
		resp.Diagnostics.AddError("Redis Configuration Update Failed", "State was preserved. "+err.Error())
		return
	}
	domain, password, err := redisConnection(ctx, redis.client, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Redis Configuration Update Failed", "Configuration may have been applied, but connection details could not be read. State was preserved. "+err.Error())
		return
	}
	data.Domain = types.StringValue(domain)
	data.Password = types.StringValue(password)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (redis *RedisResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data RedisResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	id := data.ID.ValueString()
	_, err := redis.client.JSON(ctx, "service-delete", "--service="+id, "--confirm")
	if err != nil && !errors.Is(err, stackhero.ErrNotFound) {
		resp.Diagnostics.AddError("Redis Deletion Failed", "State was preserved. "+err.Error())
		return
	}
	if err == nil {
		if err := waitForRedis(ctx, redis.client, id, "deleted"); err != nil {
			resp.Diagnostics.AddError("Redis Deletion Incomplete", "State was preserved. "+err.Error())
			return
		}
	}
	if err := waitForRedisAbsence(ctx, redis.client, id); err != nil {
		resp.Diagnostics.AddError("Redis Deletion Incomplete", "State was preserved. "+err.Error())
	}
}

func waitForRedis(ctx context.Context, client stackCLI, id, expected string) error {
	output, err := client.JSON(ctx, "service-wait-for", "--service="+id)
	if err != nil {
		return err
	}
	var response struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(output, &response); err != nil || response.State != expected {
		return fmt.Errorf("stackhero service-wait-for did not report the expected %s state", expected)
	}
	return nil
}

func redisStackID(ctx context.Context, client stackCLI, id string) (string, bool, error) {
	output, err := client.JSON(ctx, "services-list")
	if err != nil {
		return "", false, err
	}
	var response struct {
		Success *bool `json:"success"`
		Data    *[]struct {
			ID    *string `json:"id"`
			Stack *struct {
				ID *string `json:"id"`
			} `json:"stack"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &response); err != nil || response.Success == nil || !*response.Success || response.Data == nil {
		return "", false, fmt.Errorf("stackhero services-list returned an unexpected response")
	}
	for _, entry := range *response.Data {
		if entry.ID == nil || *entry.ID == "" || entry.Stack == nil || entry.Stack.ID == nil || *entry.Stack.ID == "" {
			return "", false, fmt.Errorf("stackhero services-list returned incomplete service data")
		}
		if *entry.ID == id {
			return *entry.Stack.ID, true, nil
		}
	}
	return "", false, nil
}

func redisConnection(ctx context.Context, client stackCLI, id string) (string, string, error) {
	output, err := client.JSON(ctx, "service-configuration-get", "--service="+id)
	if err != nil {
		return "", "", err
	}
	var response struct {
		Configuration *struct {
			Domain      *string `json:"domain"`
			Credentials *struct {
				Password *string `json:"password"`
			} `json:"credentials"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(output, &response); err != nil || response.Configuration == nil || response.Configuration.Domain == nil || response.Configuration.Credentials == nil || response.Configuration.Credentials.Password == nil {
		return "", "", fmt.Errorf("stackhero service-configuration-get returned incomplete connection details")
	}
	return *response.Configuration.Domain, *response.Configuration.Credentials.Password, nil
}

func waitForRedisAbsence(ctx context.Context, client stackCLI, id string) error {
	for {
		_, found, err := redisStackID(ctx, client, id)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
