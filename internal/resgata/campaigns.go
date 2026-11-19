package resgata

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type TemplateComponent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type TemplateVariables struct {
	Count        int   `json:"count"`
	Placeholders []int `json:"placeholders"`
}

type Template struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Category        string              `json:"category"`
	Language        string              `json:"language"`
	Status          string              `json:"status"`
	RejectionReason *string             `json:"rejection_reason"`
	Components      []TemplateComponent `json:"components"`
	Variables       TemplateVariables   `json:"variables"`
	CreatedAt       int64               `json:"created_at"`
	UpdatedAt       int64               `json:"updated_at"`
}

func (t Template) Component(kind string) string {
	for _, c := range t.Components {
		if strings.EqualFold(c.Type, kind) {
			return c.Text
		}
	}
	return ""
}

func (t Template) Body() string { return t.Component("BODY") }

func (t Template) Usable() bool {
	return t.Status == "approved" || t.Status == "reinstated"
}

func (t Template) Rejection() string {
	if t.RejectionReason == nil {
		return ""
	}
	return *t.RejectionReason
}

var placeholderPattern = regexp.MustCompile(`\{\{\s*(\d+)\s*\}\}`)

func Placeholders(text string) []int {
	seen := map[int]bool{}
	var out []int
	for _, match := range placeholderPattern.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(match[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

type TemplateInput struct {
	Name         string
	Category     string
	Language     string
	BodyText     string
	BodyExamples []string
	Footer       string
}

func (c *Client) Templates(ctx context.Context, token, status string) ([]Template, error) {
	path := "/templates?limit=100"
	if status != "" {
		path += "&status=" + url.QueryEscape(status)
	}
	var out []Template
	err := c.do(ctx, http.MethodGet, path, token, nil, &out)
	return out, err
}

func (c *Client) Template(ctx context.Context, token, id string) (Template, error) {
	var out Template
	err := c.do(ctx, http.MethodGet, "/templates/"+url.PathEscape(id), token, nil, &out)
	return out, err
}

func (c *Client) CreateTemplate(ctx context.Context, token string, in TemplateInput) (Template, error) {
	body := map[string]any{
		"name":     in.Name,
		"category": in.Category,
		"language": in.Language,
		"body":     map[string]any{"text": in.BodyText, "examples": in.BodyExamples},
	}
	if in.Footer != "" {
		body["footer"] = in.Footer
	}
	var out Template
	err := c.do(ctx, http.MethodPost, "/templates", token, body, &out)
	return out, err
}

type AudienceContact struct {
	Phone     string   `json:"phone"`
	Name      string   `json:"name,omitempty"`
	Variables []string `json:"variables,omitempty"`
}

type Audience struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Contacts struct {
		Received int `json:"received"`
		Accepted int `json:"accepted"`
		Rejected int `json:"rejected"`
	} `json:"contacts"`
	Rejections map[string]int `json:"rejections"`
}

func (c *Client) CreateAudience(ctx context.Context, token, name string, contacts []AudienceContact) (Audience, error) {
	var out Audience
	err := c.do(ctx, http.MethodPost, "/audiences", token, map[string]any{"name": name, "contacts": contacts}, &out)
	return out, err
}

type CampaignProgress struct {
	Total     int `json:"total"`
	Sent      int `json:"sent"`
	Delivered int `json:"delivered"`
	Read      int `json:"read"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	Pending   int `json:"pending"`
	Percent   int `json:"percent"`
}

type Campaign struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Status      string           `json:"status"`
	PauseReason *string          `json:"pause_reason"`
	TemplateID  string           `json:"template_id"`
	AudienceID  string           `json:"audience_id"`
	Progress    CampaignProgress `json:"progress"`
	CreatedAt   int64            `json:"created_at"`
	FinishedAt  *int64           `json:"finished_at"`
}

func (c Campaign) PauseText() string {
	if c.PauseReason == nil {
		return ""
	}
	return *c.PauseReason
}

type CampaignInput struct {
	Name       string
	TemplateID string
	AudienceID string
	Variables  map[string]string
}

func (c *Client) CreateCampaign(ctx context.Context, token string, in CampaignInput) (Campaign, error) {
	body := map[string]any{"name": in.Name, "template_id": in.TemplateID, "audience_id": in.AudienceID}
	if len(in.Variables) > 0 {
		body["variables"] = in.Variables
	}
	var out Campaign
	err := c.do(ctx, http.MethodPost, "/campaigns", token, body, &out)
	return out, err
}

func (c *Client) Campaign(ctx context.Context, token, id string) (Campaign, error) {
	var out Campaign
	err := c.do(ctx, http.MethodGet, "/campaigns/"+url.PathEscape(id), token, nil, &out)
	return out, err
}

func (c *Client) Campaigns(ctx context.Context, token string) ([]Campaign, error) {
	var out []Campaign
	err := c.do(ctx, http.MethodGet, "/campaigns?limit=100", token, nil, &out)
	return out, err
}

func (c *Client) PauseCampaign(ctx context.Context, token, id string) (Campaign, error) {
	var out Campaign
	err := c.do(ctx, http.MethodPost, "/campaigns/"+url.PathEscape(id)+"/pause", token, nil, &out)
	return out, err
}

func (c *Client) ResumeCampaign(ctx context.Context, token, id string) (Campaign, error) {
	var out Campaign
	err := c.do(ctx, http.MethodPost, "/campaigns/"+url.PathEscape(id)+"/resume", token, nil, &out)
	return out, err
}

func (c *Client) StartMetaLink(ctx context.Context, token, returnURL, simulate string) (string, error) {
	body := map[string]string{"return_url": returnURL}
	if simulate != "" {
		body["simulate"] = simulate
	}
	var out struct {
		URL string `json:"url"`
	}
	err := c.do(ctx, http.MethodPost, "/connection/meta-link", token, body, &out)
	return out.URL, err
}
