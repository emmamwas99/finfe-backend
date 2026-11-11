package paystack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client calls the Paystack REST API.
type Client struct {
	SecretKey string
	BaseURL   string
	HTTP      *http.Client
}

func NewClient(secret string) *Client {
	return &Client{SecretKey: secret, BaseURL: "https://api.paystack.co", HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("paystack %s %s: %d %s", method, path, res.StatusCode, e.Message)
	}
	return json.Unmarshal(raw, out)
}

// InitializeInput starts a hosted checkout.
type InitializeInput struct {
	Email       string   `json:"email"`
	AmountMinor int64    `json:"amount"`
	Currency    string   `json:"currency"`
	Reference   string   `json:"reference"`
	Channels    []string `json:"channels"`
	Plan        string   `json:"plan,omitempty"`
	CallbackURL string   `json:"callback_url"`
	Metadata    string   `json:"metadata"`
}

func (c *Client) Initialize(ctx context.Context, in InitializeInput) (authorizationURL string, err error) {
	var out struct {
		Data struct {
			AuthorizationURL string `json:"authorization_url"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/transaction/initialize", in, &out); err != nil {
		return "", err
	}
	return out.Data.AuthorizationURL, nil
}

// Verify fetches a transaction's current state.
func (c *Client) Verify(ctx context.Context, reference string) (EventData, error) {
	var out struct {
		Data EventData `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/transaction/verify/"+url.PathEscape(reference), nil, &out)
	return out.Data, err
}

// ManageLink returns Paystack's hosted page where a card subscriber updates the card or cancels.
func (c *Client) ManageLink(ctx context.Context, subscriptionCode string) (string, error) {
	var out struct {
		Data struct {
			Link string `json:"link"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/subscription/"+url.PathEscape(subscriptionCode)+"/manage/link", nil, &out)
	return out.Data.Link, err
}

// Disable stops an auto-renewing subscription.
func (c *Client) Disable(ctx context.Context, code, emailToken string) error {
	var out map[string]any
	return c.do(ctx, http.MethodPost, "/subscription/disable", map[string]string{"code": code, "token": emailToken}, &out)
}
