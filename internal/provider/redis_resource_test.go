package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakeRedisCLI struct {
	responses  map[string]json.RawMessage
	err        error
	commands   []string
	setArgs    []string
	deleteArgs map[string][]string
}

func (fake *fakeRedisCLI) JSON(ctx context.Context, command string, args ...string) (json.RawMessage, error) {
	fake.commands = append(fake.commands, command)
	if command == "service-delete" || command == "stack-delete" {
		if fake.deleteArgs == nil {
			fake.deleteArgs = make(map[string][]string)
		}
		fake.deleteArgs[command] = args
	}
	if command == "service-configuration-set" && len(args) == 2 {
		fake.setArgs = args
		fake.responses["service-configuration-get"] = json.RawMessage(`{"success":true,"configuration":` + strings.TrimPrefix(args[1], "--configuration=") + `}`)
	}
	if command == "service-delete" {
		fake.responses["service-wait-for"] = json.RawMessage(`{"success":true,"state":"deleted"}`)
		fake.responses["services-list"] = json.RawMessage(`{"success":true,"data":[]}`)
	}
	return fake.responses[command], fake.err
}

func (fake *fakeRedisCLI) Script(ctx context.Context, command string, args ...string) (string, error) {
	fake.commands = append(fake.commands, command)
	return "svc-123", fake.err
}

func TestRedisCLIResponses(t *testing.T) {
	client := &fakeRedisCLI{responses: map[string]json.RawMessage{
		"services-list":             json.RawMessage(`{"success":true,"data":[{"id":"svc-123","stack":{"id":"stk-123"},"status":{"state":"running"}}]}`),
		"service-wait-for":          json.RawMessage(`{"success":true,"state":"running"}`),
		"service-configuration-get": json.RawMessage(`{"success":true,"configuration":{"domain":"redis.example","credentials":{"password":"secret"}}}`),
	}}
	stackID, found, err := redisStackID(context.Background(), client, "svc-123")
	if err != nil || !found || stackID != "stk-123" {
		t.Fatalf("unexpected service list result: %q, %v, %v", stackID, found, err)
	}
	if err := waitForRedis(context.Background(), client, "svc-123", "running"); err != nil {
		t.Fatal(err)
	}
	if err := waitForRedis(context.Background(), client, "svc-123", "deleted"); err == nil {
		t.Fatal("running must not be accepted as deleted")
	}
	domain, password, err := redisConnection(context.Background(), client, "svc-123")
	if err != nil || domain != "redis.example" || password != "secret" {
		t.Fatal("unexpected connection details")
	}
}

func TestRedisRejectsIncompleteList(t *testing.T) {
	client := &fakeRedisCLI{responses: map[string]json.RawMessage{"services-list": json.RawMessage(`{"success":true,"data":[{"id":"svc-123"}]}`)}}
	if _, _, err := redisStackID(context.Background(), client, "svc-123"); err == nil {
		t.Fatal("expected an error for incomplete service data")
	}
}

func TestRedisAbsenceWaitHonorsCancellation(t *testing.T) {
	client := &fakeRedisCLI{responses: map[string]json.RawMessage{"services-list": json.RawMessage(`{"success":true,"data":[{"id":"svc-123","stack":{"id":"stk-123"}}]}`)}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRedisAbsence(ctx, client, "svc-123"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation while service remains listed, got %v", err)
	}
}

func TestLiveCleanupOnlyDeletesItsOwnResources(t *testing.T) {
	client := &fakeRedisCLI{responses: map[string]json.RawMessage{
		"stacks-list":      json.RawMessage(`{"success":true,"data":[{"id":"stk-123","name":"test-stack"},{"id":"stk-other","name":"other"}]}`),
		"services-list":    json.RawMessage(`{"success":true,"data":[{"id":"svc-other","name":"Redis","stack":{"id":"stk-other"}},{"id":"svc-123","name":"Redis","stack":{"id":"stk-123"}}]}`),
		"service-delete":   json.RawMessage(`{"success":true}`),
		"stack-delete":     json.RawMessage(`{"success":true}`),
		"service-wait-for": json.RawMessage(`{"success":true,"state":"deleted"}`),
	}}
	cleanupLiveRedis(t, client, "test-stack")
	if got := strings.Join(client.commands, ","); got != "stacks-list,services-list,service-delete,service-wait-for,services-list,stack-delete" {
		t.Fatalf("unexpected cleanup commands: %s", got)
	}
	if strings.Join(client.deleteArgs["service-delete"], ",") != "--service=svc-123,--confirm" || strings.Join(client.deleteArgs["stack-delete"], ",") != "--stack=stk-123,--confirm" {
		t.Fatal("cleanup targeted a resource outside the test stack")
	}
}

func TestRedisResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	client := &fakeRedisCLI{responses: map[string]json.RawMessage{
		"services-list":             json.RawMessage(`{"success":true,"data":[{"id":"svc-123","stack":{"id":"stk-123"}}]}`),
		"service-wait-for":          json.RawMessage(`{"success":true,"state":"running"}`),
		"service-configuration-get": json.RawMessage(`{"success":true,"configuration":{"domain":"redis.example","credentials":{"password":"secret"},"tlsOnly":false}}`),
		"service-configuration-set": json.RawMessage(`{"success":true}`),
		"service-delete":            json.RawMessage(`{"success":true}`),
	}}
	redis := &RedisResource{client: client}
	var schemaResp resource.SchemaResponse
	redis.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	objectType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "stack_id": tftypes.String, "instance": tftypes.String,
		"region": tftypes.String, "organization": tftypes.String, "domain": tftypes.String,
		"password": tftypes.String, "configuration_json": tftypes.String,
	}}
	plan := tftypes.NewValue(objectType, map[string]tftypes.Value{
		"id":                 tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"stack_id":           tftypes.NewValue(tftypes.String, "stk-123"),
		"instance":           tftypes.NewValue(tftypes.String, "1G"),
		"region":             tftypes.NewValue(tftypes.String, "europe"),
		"organization":       tftypes.NewValue(tftypes.String, nil),
		"domain":             tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"password":           tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"configuration_json": tftypes.NewValue(tftypes.String, `{"tlsOnly":true}`),
	})
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	redis.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schemaResp.Schema, Raw: plan}}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}
	var created RedisResourceModel
	if diags := createResp.State.Get(ctx, &created); diags.HasError() || created.ID.ValueString() != "svc-123" || created.Domain.ValueString() != "redis.example" || created.Password.ValueString() != "secret" {
		t.Fatal("created state did not contain expected connection details")
	}
	if len(client.setArgs) != 2 || !strings.Contains(client.setArgs[1], `"password":"secret"`) || !strings.Contains(client.setArgs[1], `"tlsOnly":true`) {
		t.Fatal("configuration update did not preserve credentials and managed setting")
	}

	readResp := resource.ReadResponse{State: createResp.State}
	redis.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}

	var deleteResp resource.DeleteResponse
	redis.Delete(ctx, resource.DeleteRequest{State: readResp.State}, &deleteResp)
	if deleteResp.Diagnostics.HasError() || client.commands[len(client.commands)-1] != "services-list" {
		t.Fatalf("delete did not confirm service absence: %v", deleteResp.Diagnostics)
	}
}
