package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakeStackCLI struct {
	output  json.RawMessage
	err     error
	command string
	args    []string
}

func (fake *fakeStackCLI) JSON(ctx context.Context, command string, args ...string) (json.RawMessage, error) {
	fake.command = command
	fake.args = args
	return fake.output, fake.err
}

func (fake *fakeStackCLI) Script(ctx context.Context, command string, args ...string) (string, error) {
	fake.command = command
	fake.args = args
	return "stk-123", fake.err
}

func TestStackName(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		err     error
		want    string
		found   bool
		wantErr bool
	}{
		{name: "found", output: `{"success":true,"data":[{"id":"stk-123","name":"example","servicesCount":0}]}`, want: "example", found: true},
		{name: "missing", output: `{"success":true,"data":[]}`},
		{name: "missing list", output: `{"success":true}`, wantErr: true},
		{name: "missing name", output: `{"success":true,"data":[{"id":"stk-123"}]}`, wantErr: true},
		{name: "failed response", output: `{"success":false,"data":[]}`, wantErr: true},
		{name: "command error", err: errors.New("command failed"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeStackCLI{output: json.RawMessage(test.output), err: test.err}
			name, found, err := stackName(context.Background(), client, "stk-123")
			if (err != nil) != test.wantErr || name != test.want || found != test.found || client.command != "stacks-list" {
				t.Fatalf("got name=%q, found=%v, error=%v, command=%q", name, found, err, client.command)
			}
		})
	}
}

func TestStackCreateReadAndDelete(t *testing.T) {
	ctx := context.Background()
	client := &fakeStackCLI{}
	stack := &StackResource{client: client}
	var schemaResp resource.SchemaResponse
	stack.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	objectType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "name": tftypes.String, "organization": tftypes.String,
	}}
	plan := tftypes.NewValue(objectType, map[string]tftypes.Value{
		"id":           tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"name":         tftypes.NewValue(tftypes.String, "example"),
		"organization": tftypes.NewValue(tftypes.String, "org-123"),
	})
	createReq := resource.CreateRequest{Plan: tfsdk.Plan{Schema: schemaResp.Schema, Raw: plan}}
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	stack.Create(ctx, createReq, &createResp)
	if createResp.Diagnostics.HasError() || client.command != "stack-create" || len(client.args) != 2 || client.args[0] != "--name=example" || client.args[1] != "--organization=org-123" {
		t.Fatalf("unexpected create result: %v, %s %v", createResp.Diagnostics, client.command, client.args)
	}
	var created StackResourceModel
	if diags := createResp.State.Get(ctx, &created); diags.HasError() || created.ID.ValueString() != "stk-123" {
		t.Fatalf("unexpected created state: %v, %v", created, diags)
	}

	client.output = json.RawMessage(`{"success":true,"data":[{"id":"stk-123","name":"renamed"}]}`)
	readReq := resource.ReadRequest{State: createResp.State}
	readResp := resource.ReadResponse{State: createResp.State}
	stack.Read(ctx, readReq, &readResp)
	var refreshed StackResourceModel
	if diags := readResp.State.Get(ctx, &refreshed); readResp.Diagnostics.HasError() || diags.HasError() || refreshed.Name.ValueString() != "renamed" {
		t.Fatalf("unexpected refreshed state: %v, %v, %v", refreshed, readResp.Diagnostics, diags)
	}

	deleteReq := resource.DeleteRequest{State: readResp.State}
	var deleteResp resource.DeleteResponse
	stack.Delete(ctx, deleteReq, &deleteResp)
	if deleteResp.Diagnostics.HasError() || client.command != "stack-delete" || len(client.args) != 2 || client.args[0] != "--stack=stk-123" || client.args[1] != "--confirm" {
		t.Fatalf("unexpected delete result: %v, %s %v", deleteResp.Diagnostics, client.command, client.args)
	}
}

func TestStackReadPreservesMissingState(t *testing.T) {
	ctx := context.Background()
	client := &fakeStackCLI{output: json.RawMessage(`{"success":true,"data":[]}`)}
	stack := &StackResource{client: client}
	var schemaResp resource.SchemaResponse
	stack.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	objectType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "name": tftypes.String, "organization": tftypes.String,
	}}
	state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objectType, map[string]tftypes.Value{
		"id":           tftypes.NewValue(tftypes.String, "stk-123"),
		"name":         tftypes.NewValue(tftypes.String, "example"),
		"organization": tftypes.NewValue(tftypes.String, nil),
	})}
	response := resource.ReadResponse{State: state}
	stack.Read(ctx, resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatal("missing stack must produce an error and preserve state")
	}
}
