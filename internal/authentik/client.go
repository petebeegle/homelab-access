package authentik

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
	token      string
	httpClient *http.Client
}

type User struct {
	ID       int    `json:"pk,omitempty"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	IsActive bool   `json:"is_active"`
}

type usersListResponse struct {
	Results []User `json:"results"`
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) EnsureUser(ctx context.Context, request access.Request) (User, bool, error) {
	if c.baseURL == "" || c.token == "" {
		return User{}, false, errors.New("authentik base URL and token are required")
	}
	if request.DiscordUserID == "" {
		return User{}, false, errors.New("discord user id is required")
	}

	username := "discord-" + request.DiscordUserID
	user, found, err := c.findUser(ctx, username)
	if err != nil {
		return User{}, false, err
	}
	if found {
		return user, false, nil
	}

	created, err := c.createUser(ctx, User{
		Username: username,
		Name:     request.DiscordName,
		IsActive: true,
	})
	if err != nil {
		return User{}, false, err
	}
	return created, true, nil
}

func (c *Client) findUser(ctx context.Context, username string) (User, bool, error) {
	endpoint, err := url.Parse(c.baseURL + "/api/v3/core/users/")
	if err != nil {
		return User{}, false, err
	}
	query := endpoint.Query()
	query.Set("search", username)
	endpoint.RawQuery = query.Encode()

	request, err := c.newRequest(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return User{}, false, err
	}

	var response usersListResponse
	if err := c.do(request, http.StatusOK, &response); err != nil {
		return User{}, false, err
	}
	for _, user := range response.Results {
		if user.Username == username {
			return user, true, nil
		}
	}
	return User{}, false, nil
}

func (c *Client) createUser(ctx context.Context, user User) (User, error) {
	body, err := json.Marshal(map[string]any{
		"username":  user.Username,
		"name":      user.Name,
		"is_active": user.IsActive,
	})
	if err != nil {
		return User{}, err
	}

	request, err := c.newRequest(ctx, http.MethodPost, c.baseURL+"/api/v3/core/users/", bytes.NewReader(body))
	if err != nil {
		return User{}, err
	}

	var response User
	if err := c.do(request, http.StatusCreated, &response); err != nil {
		return User{}, err
	}
	return response, nil
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
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
		return fmt.Errorf("authentik returned status %d: %s", response.StatusCode, string(body))
	}
	if output == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, output); err != nil {
		return err
	}
	return nil
}
