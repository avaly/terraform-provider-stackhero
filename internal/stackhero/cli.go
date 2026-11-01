package stackhero

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
)

var ErrNotFound = errors.New("resource not found")
var ErrNotEmpty = errors.New("stack is not empty")

type Client struct {
	path  string
	token string
}

func NewClient(path, token string) *Client {
	return &Client{path: path, token: token}
}

func (client *Client) CheckVersion(ctx context.Context) error {
	output, err := client.JSON(ctx, "--help")
	if err != nil {
		return err
	}
	var response struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(output, &response) != nil {
		return errors.New("stackhero returned invalid version information")
	}
	current, err := version.NewVersion(response.Version)
	if err != nil {
		return errors.New("stackhero returned invalid version information")
	}
	minimum := version.Must(version.NewVersion("1.2.0"))
	if current.LessThan(minimum) {
		return errors.New("stackhero CLI version 1.2.0 or later is required")
	}
	return nil
}

func (client *Client) JSON(ctx context.Context, command string, args ...string) (json.RawMessage, error) {
	output, runErr := client.run(ctx, "json", command, args...)
	if runErr != nil {
		return nil, runErr
	}

	if !json.Valid(output) {
		return nil, fmt.Errorf("stackhero %s returned invalid JSON", command)
	}

	var response struct {
		Success *bool `json:"success"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("stackhero %s returned an unexpected JSON response", command)
	}
	if response.Success == nil {
		return nil, fmt.Errorf("stackhero %s returned an unexpected JSON response", command)
	}
	if !*response.Success {
		return nil, cliError(command, response.Error.Code)
	}

	return json.RawMessage(output), nil
}

func (client *Client) Script(ctx context.Context, command string, args ...string) (string, error) {
	output, err := client.run(ctx, "script", command, args...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" || strings.ContainsAny(value, " \t\r\n{}[]\"") {
		return "", fmt.Errorf("stackhero %s returned an invalid identifier", command)
	}
	return value, nil
}

func (client *Client) run(ctx context.Context, format, command string, args ...string) ([]byte, error) {
	timeout := 2 * time.Minute
	if command == "service-wait-for" {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	arguments := append([]string{"--format=" + format, command}, args...)
	process := exec.CommandContext(ctx, client.path, arguments...)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "STACKHERO_TOKEN") {
			process.Env = append(process.Env, entry)
		}
	}
	process.Env = append(process.Env, "STACKHERO_TOKEN="+client.token)

	output, err := process.Output()
	if err == nil {
		return output, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("stackhero %s: %w", command, ctx.Err())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(output, &response) == nil && response.Error.Code != "" {
		return nil, cliError(command, response.Error.Code)
	}
	return nil, fmt.Errorf("stackhero %s failed", command)
}

func cliError(command, code string) error {
	switch code {
	case "unauthorized":
		return fmt.Errorf("stackhero %s failed (code: %s)", command, code)
	case "notFound":
		return fmt.Errorf("stackhero %s: %w", command, ErrNotFound)
	case "notEmptyStack":
		return fmt.Errorf("stackhero %s: %w", command, ErrNotEmpty)
	}
	return errors.New("stackhero " + command + " failed")
}
