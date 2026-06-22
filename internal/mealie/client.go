// Package mealie is a minimal read client for the Mealie recipe manager REST API.
package mealie

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a client for the Mealie instance at baseURL, authenticating with
// a long-lived API token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

type Tag struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

type Food struct {
	Name string `json:"name"`
}

type Unit struct {
	Name         string `json:"name"`
	Abbreviation string `json:"abbreviation"`
}

type Ingredient struct {
	Quantity float64 `json:"quantity"`
	Unit     *Unit   `json:"unit"`
	Food     *Food   `json:"food"`
	Note     string  `json:"note"`
	Display  string  `json:"display"`
}

type Instruction struct {
	Text string `json:"text"`
}

type Recipe struct {
	Slug               string        `json:"slug"`
	Name               string        `json:"name"`
	Description        string        `json:"description"`
	Tags               []Tag         `json:"tags"`
	RecipeIngredient   []Ingredient  `json:"recipeIngredient"`
	RecipeInstructions []Instruction `json:"recipeInstructions"`
}

type Summary struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Tags        []Tag  `json:"tags"`
}

func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mealie %s %s: status %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.request(ctx, http.MethodGet, path, nil, out)
}

// CreateRecipe creates a stub recipe by name and returns its slug.
func (c *Client) CreateRecipe(ctx context.Context, name string) (string, error) {
	var slug string
	if err := c.request(ctx, http.MethodPost, "/api/recipes", map[string]string{"name": name}, &slug); err != nil {
		return "", err
	}
	return slug, nil
}

// EnsureTag returns an existing tag by name or creates it.
func (c *Client) EnsureTag(ctx context.Context, name string) (Tag, error) {
	var created Tag
	err := c.request(ctx, http.MethodPost, "/api/organizers/tags", map[string]string{"name": name}, &created)
	if err == nil && created.ID != "" {
		return created, nil
	}
	var resp struct {
		Items []Tag `json:"items"`
	}
	if e := c.get(ctx, "/api/organizers/tags?search="+url.QueryEscape(name), &resp); e == nil {
		for _, t := range resp.Items {
			if strings.EqualFold(t.Name, name) {
				return t, nil
			}
		}
	}
	if err == nil {
		err = fmt.Errorf("tag %q not found after create", name)
	}
	return Tag{}, err
}

// PatchRecipe applies a partial update to a recipe.
func (c *Client) PatchRecipe(ctx context.Context, slug string, payload any) error {
	return c.request(ctx, http.MethodPatch, "/api/recipes/"+url.PathEscape(slug), payload, nil)
}

// SearchRecipes returns up to perPage recipe summaries matching query.
func (c *Client) SearchRecipes(ctx context.Context, query string, perPage int) ([]Summary, error) {
	q := url.Values{}
	if query != "" {
		q.Set("search", query)
	}
	q.Set("perPage", strconv.Itoa(perPage))
	q.Set("page", "1")
	var resp struct {
		Items []Summary `json:"items"`
	}
	if err := c.get(ctx, "/api/recipes?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// GetRecipe returns the full recipe for a slug.
func (c *Client) GetRecipe(ctx context.Context, slug string) (*Recipe, error) {
	var r Recipe
	if err := c.get(ctx, "/api/recipes/"+url.PathEscape(slug), &r); err != nil {
		return nil, err
	}
	return &r, nil
}
