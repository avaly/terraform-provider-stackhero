package stackhero

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMain(testMain *testing.M) {
	if os.Getenv("STACKHERO_CLI_TEST_PROCESS") == "1" {
		switch os.Getenv("STACKHERO_CLI_TEST_RESULT") {
		case "failure":
			fmt.Fprint(os.Stdout, `{"success":false,"error":{"code":"unauthorized","message":"secret-token"}}`)
			fmt.Fprint(os.Stderr, "secret-token")
			os.Exit(1)
		case "json-failure":
			fmt.Fprint(os.Stdout, `{"success":false,"error":{"code":"secret_token"}}`)
		case "not-found":
			fmt.Fprint(os.Stdout, `{"success":false,"error":{"code":"notFound"}}`)
			os.Exit(1)
		case "invalid":
			fmt.Fprint(os.Stdout, "invalid secret-token")
		case "missing-success":
			fmt.Fprint(os.Stdout, `{"data":[]}`)
		case "script":
			fmt.Fprint(os.Stdout, "stk-123\n")
		case "version":
			fmt.Fprint(os.Stdout, `{"success":true,"version":"v1.2.0"}`)
		case "old-version":
			fmt.Fprint(os.Stdout, `{"success":true,"version":"v1.1.0"}`)
		default:
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"success": true,
				"args":    os.Args[1:],
				"token":   os.Getenv("STACKHERO_TOKEN"),
			})
		}
		os.Exit(0)
	}
	os.Exit(testMain.Run())
}

func testClient(t *testing.T, result string) *Client {
	t.Helper()
	t.Setenv("STACKHERO_CLI_TEST_PROCESS", "1")
	t.Setenv("STACKHERO_CLI_TEST_RESULT", result)
	t.Setenv("STACKHERO_TOKEN", "old-token")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(path, "secret-token")
}

func TestJSONRunsWithIsolatedTokenAndArguments(t *testing.T) {
	client := testClient(t, "success")
	output, err := client.JSON(context.Background(), "services-list", "--stack=one;two")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Args  []string `json:"args"`
		Token string   `json:"token"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	if response.Token != "secret-token" || len(response.Args) != 3 || response.Args[0] != "--format=json" || response.Args[1] != "services-list" || response.Args[2] != "--stack=one;two" {
		t.Fatalf("unexpected command arguments or token")
	}
	if os.Getenv("STACKHERO_TOKEN") != "old-token" {
		t.Fatal("parent environment was modified")
	}
}

func TestJSONRedactsCLIError(t *testing.T) {
	client := testClient(t, "failure")
	_, err := client.JSON(context.Background(), "services-list")
	if err == nil || !strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestJSONRejectsInvalidOutput(t *testing.T) {
	client := testClient(t, "invalid")
	_, err := client.JSON(context.Background(), "services-list")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestJSONRedactsUnrecognizedErrorCodes(t *testing.T) {
	client := testClient(t, "json-failure")
	_, err := client.JSON(context.Background(), "services-list")
	if err == nil || strings.Contains(err.Error(), "secret_token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestJSONRequiresSuccessField(t *testing.T) {
	client := testClient(t, "missing-success")
	_, err := client.JSON(context.Background(), "stack-delete")
	if err == nil {
		t.Fatal("expected an error for a response without a success field")
	}
}

func TestJSONClassifiesNotFound(t *testing.T) {
	client := testClient(t, "not-found")
	_, err := client.JSON(context.Background(), "stack-delete")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestScriptReturnsIdentifier(t *testing.T) {
	client := testClient(t, "script")
	id, err := client.Script(context.Background(), "stack-create", "--name=example")
	if err != nil || id != "stk-123" {
		t.Fatalf("unexpected result %q: %v", id, err)
	}
}

func TestCanceledContext(t *testing.T) {
	client := testClient(t, "success")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.JSON(ctx, "services-list")
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLIVersion(t *testing.T) {
	for _, test := range []struct {
		result  string
		wantErr bool
	}{
		{result: "version"},
		{result: "old-version", wantErr: true},
		{result: "success", wantErr: true},
	} {
		t.Run(test.result, func(t *testing.T) {
			client := testClient(t, test.result)
			if err := client.CheckVersion(context.Background()); (err != nil) != test.wantErr {
				t.Fatalf("unexpected version check result: %v", err)
			}
		})
	}
}
