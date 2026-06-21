package cocktaildb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchParsesDrinks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("s") == "" {
			t.Errorf("missing search query")
		}
		_, _ = w.Write([]byte(`{"drinks":[{"strDrink":"Negroni","strCategory":"Cocktail","strAlcoholic":"Alcoholic","strGlass":"Old-Fashioned glass","strInstructions":"Stir.","strIBA":"Unforgettables","strIngredient1":"Gin","strMeasure1":"1 oz","strIngredient2":"Campari","strMeasure2":"1 oz","strIngredient3":"Sweet Vermouth","strMeasure3":"1 oz","strIngredient4":null,"strMeasure4":null}]}`))
	}))
	defer ts.Close()

	recipes, err := NewWithBaseURL(ts.URL).Search(context.Background(), "negroni")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(recipes) != 1 {
		t.Fatalf("expected 1 recipe, got %d", len(recipes))
	}
	r := recipes[0]
	if r.Name != "Negroni" || r.IBA != "Unforgettables" {
		t.Fatalf("unexpected recipe: %+v", r)
	}
	if len(r.Ingredients) != 3 {
		t.Fatalf("expected 3 ingredients, got %d: %+v", len(r.Ingredients), r.Ingredients)
	}
	if r.Ingredients[0].Name != "Gin" || r.Ingredients[0].Measure != "1 oz" {
		t.Fatalf("unexpected first ingredient: %+v", r.Ingredients[0])
	}
}

func TestSearchNoResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"drinks":null}`))
	}))
	defer ts.Close()

	recipes, err := NewWithBaseURL(ts.URL).Search(context.Background(), "nope")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(recipes) != 0 {
		t.Fatalf("expected 0 recipes, got %d", len(recipes))
	}
}
