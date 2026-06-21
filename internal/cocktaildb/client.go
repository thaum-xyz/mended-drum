// Package cocktaildb is a minimal client for TheCocktailDB free API, used as a
// fallback recipe source when a cocktail is not in the bar's own (Mealie) book.
package cocktaildb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a client for the public TheCocktailDB API (test key "1").
func New() *Client { return NewWithBaseURL("https://www.thecocktaildb.com/api/json/v1/1") }

// NewWithBaseURL is used in tests to point at a stub server.
func NewWithBaseURL(base string) *Client {
	return &Client{baseURL: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

type Ingredient struct {
	Name    string `json:"name"`
	Measure string `json:"measure,omitempty"`
}

type Recipe struct {
	Name         string       `json:"name"`
	Category     string       `json:"category,omitempty"`
	Alcoholic    string       `json:"alcoholic,omitempty"`
	Glass        string       `json:"glass,omitempty"`
	Instructions string       `json:"instructions,omitempty"`
	IBA          string       `json:"iba,omitempty"`
	Ingredients  []Ingredient `json:"ingredients"`
}

// Search looks up cocktails by (partial) name.
func (c *Client) Search(ctx context.Context, name string) ([]Recipe, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/search.php?s="+url.QueryEscape(name), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("thecocktaildb status %d", resp.StatusCode)
	}
	var body struct {
		Drinks []map[string]any `json:"drinks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	out := make([]Recipe, 0, len(body.Drinks))
	for _, d := range body.Drinks {
		r := Recipe{
			Name:         str(d, "strDrink"),
			Category:     str(d, "strCategory"),
			Alcoholic:    str(d, "strAlcoholic"),
			Glass:        str(d, "strGlass"),
			Instructions: str(d, "strInstructions"),
			IBA:          str(d, "strIBA"),
		}
		for i := 1; i <= 15; i++ {
			ing := strings.TrimSpace(str(d, fmt.Sprintf("strIngredient%d", i)))
			if ing == "" {
				continue
			}
			r.Ingredients = append(r.Ingredients, Ingredient{
				Name:    ing,
				Measure: strings.TrimSpace(str(d, fmt.Sprintf("strMeasure%d", i))),
			})
		}
		out = append(out, r)
	}
	return out, nil
}

// str safely reads a string field from a decoded drink (values are strings or null).
func str(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
