package pushover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vrchat-join-notification-with-pushover/internal/config"
	"vrchat-join-notification-with-pushover/internal/core"
	"vrchat-join-notification-with-pushover/internal/logger"
)

// Client sends messages to the Pushover API.
type Client struct {
	cfg    *config.Config
	log    *logger.Logger
	client *http.Client
}

// New creates a new Pushover client.
func New(cfg *config.Config, log *logger.Logger) *Client {
	return &Client{
		cfg:    cfg,
		log:    log,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

// Send posts a notification if credentials are configured.
func (c *Client) Send(title, message string) {
	if c == nil {
		return
	}
	if c.cfg == nil {
		if c.log != nil {
			c.log.Log("Pushover configuration unavailable; skipping.")
		}
		return
	}
	token := strings.TrimSpace(c.cfg.PushoverToken)
	user := strings.TrimSpace(c.cfg.PushoverUser)
	if token == "" || user == "" {
		if c.log != nil {
			c.log.Log("Pushover not configured; skipping.")
		}
		return
	}
	payload := url.Values{
		"token":    {token},
		"user":     {user},
		"title":    {title},
		"message":  {message},
		"priority": {"0"},
	}
	req, err := http.NewRequest(http.MethodPost, core.PushoverURL, bytes.NewBufferString(payload.Encode()))
	if err != nil {
		if c.log != nil {
			c.log.Log(fmt.Sprintf("Pushover build error: %v", err))
		}
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		if c.log != nil {
			c.log.Log(fmt.Sprintf("Pushover error: %v", err))
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		if c.log != nil {
			c.log.Log(fmt.Sprintf("Pushover API error: status %d", resp.StatusCode))
		}
		return
	}
	var decoded struct {
		Status int `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		if c.log != nil {
			c.log.Log("Pushover sent; response parsing failed.")
		}
		return
	}
	if c.log != nil {
		c.log.Log(fmt.Sprintf("Pushover sent: %d", decoded.Status))
	}
}
