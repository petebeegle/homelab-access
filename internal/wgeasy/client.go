package wgeasy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/petebeegle/homelab-access/internal/access"
)

type Client struct {
	baseURL    string
	username   string
	password   string
	session    string
	httpClient *http.Client
}

type ProvisionedClient struct {
	ID            string
	Configuration string
}

func New(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) ProvisionClient(ctx context.Context, request access.Request) (ProvisionedClient, error) {
	if c.baseURL == "" || c.username == "" || c.password == "" {
		return ProvisionedClient{}, errors.New("wg-easy base URL, username, and password are required")
	}
	if request.DiscordUserID == "" {
		return ProvisionedClient{}, errors.New("discord user id is required")
	}

	if err := c.login(ctx); err != nil {
		return ProvisionedClient{}, err
	}

	name := clientName(request)
	clientID, err := c.findClientID(ctx, name)
	if err != nil {
		return ProvisionedClient{}, err
	}
	if clientID == "" {
		clientID, err = c.createClient(ctx, name)
		if err != nil {
			return ProvisionedClient{}, err
		}
	}

	configuration, err := c.getConfiguration(ctx, clientID)
	if err != nil {
		return ProvisionedClient{}, err
	}

	return ProvisionedClient{
		ID:            clientID,
		Configuration: configuration,
	}, nil
}

func clientName(request access.Request) string {
	name := sanitizeName(request.DiscordName)
	if name == "" {
		name = sanitizeName(request.DiscordUserID)
	}

	suffix := request.DiscordUserID
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	if suffix == "" {
		return "discord-" + name
	}
	if name == "" || name == suffix {
		return "discord-" + suffix
	}
	return "discord-" + name + "-" + suffix
}

func sanitizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.' || r == ' ':
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	result := strings.Trim(builder.String(), "-")
	if len(result) > 48 {
		result = strings.Trim(result[:48], "-")
	}
	return result
}

func (c *Client) login(ctx context.Context) error {
	body, err := json.Marshal(map[string]any{
		"username": c.username,
		"password": c.password,
		"remember": true,
	})
	if err != nil {
		return err
	}

	request, err := c.newRequest(ctx, http.MethodPost, c.baseURL+"/api/session", bytes.NewReader(body))
	if err != nil {
		return err
	}

	var response struct {
		Status string `json:"status"`
	}
	if err := c.do(request, http.StatusOK, &response); err != nil {
		return err
	}
	for _, cookie := range request.Response.Cookies() {
		if cookie.Name == "wg-easy" && cookie.Value != "" {
			c.session = cookie.Name + "=" + cookie.Value
			break
		}
	}
	if c.session == "" {
		return errors.New("wg-easy did not return a session cookie")
	}
	if response.Status != "success" {
		return fmt.Errorf("wg-easy login returned status %q", response.Status)
	}
	return nil
}

func (c *Client) findClientID(ctx context.Context, name string) (string, error) {
	endpoint, err := url.Parse(c.baseURL + "/api/client")
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("filter", name)
	endpoint.RawQuery = query.Encode()

	request, err := c.newRequest(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}

	var clients []struct {
		ID       json.Number `json:"id"`
		ClientID json.Number `json:"clientId"`
		Name     string      `json:"name"`
	}
	if err := c.do(request, http.StatusOK, &clients); err != nil {
		return "", err
	}

	for _, client := range clients {
		if client.Name != name {
			continue
		}
		id := client.ClientID
		if id == "" {
			id = client.ID
		}
		return id.String(), nil
	}
	return "", nil
}

func (c *Client) createClient(ctx context.Context, name string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"name":      name,
		"expiresAt": nil,
	})
	if err != nil {
		return "", err
	}

	request, err := c.newRequest(ctx, http.MethodPost, c.baseURL+"/api/client", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	var response struct {
		Success  bool        `json:"success"`
		ClientID json.Number `json:"clientId"`
	}
	if err := c.do(request, http.StatusOK, &response); err != nil {
		return "", err
	}
	if !response.Success || response.ClientID == "" {
		return "", errors.New("wg-easy did not return created client id")
	}
	return response.ClientID.String(), nil
}

func (c *Client) getConfiguration(ctx context.Context, clientID string) (string, error) {
	request, err := c.newRequest(ctx, http.MethodGet, c.baseURL+"/api/client/"+url.PathEscape(clientID)+"/configuration", nil)
	if err != nil {
		return "", err
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wg-easy returned status %d: %s", response.StatusCode, string(body))
	}
	if len(body) == 0 {
		return "", errors.New("wg-easy returned empty configuration")
	}
	return string(body), nil
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if c.session != "" {
		request.Header.Set("Cookie", c.session)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func (c *Client) do(request *http.Request, expectedStatus int, output any) error {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("wg-easy returned status %d: %s", response.StatusCode, string(body))
	}
	request.Response = response
	if output == nil || len(body) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	return nil
}
