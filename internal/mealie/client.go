// Package mealie is a minimal read client for the Mealie recipe manager REST API.
package mealie

import (
	"context"
	"encoding/json"
	"fmt"
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
	Name string `json:"name"`
	Slug string `json:"slug"`
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

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mealie GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
