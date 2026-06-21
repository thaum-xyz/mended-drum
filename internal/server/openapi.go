package server

// openAPISpec is the OpenAPI 3.1 document Open WebUI fetches to register the
// bar tools. Descriptions are written for the LLM that selects the tools.
const openAPISpec = `{
  "openapi": "3.1.0",
  "info": {
    "title": "Mended Drum bar tools",
    "version": "0.1.0",
    "description": "Tools for the bar assistant: a 3-state ingredient inventory and the cocktail recipe book (backed by Mealie), including which recipes can be made from current stock and their allergens."
  },
  "servers": [{"url": "https://drum-tools.krupa.net.pl"}],
  "paths": {
    "/inventory": {
      "get": {
        "operationId": "listInventory",
        "summary": "List bar inventory",
        "description": "Returns every tracked ingredient with its stock status (in_stock, low or out). Ingredients not in this list are untracked.",
        "responses": {"200": {"description": "Current inventory", "content": {"application/json": {"schema": {"type": "object"}}}}}
      },
      "put": {
        "operationId": "setInventoryStatus",
        "summary": "Set an ingredient's stock status",
        "description": "Create or update the stock status of a single ingredient. Use the ingredient name as it appears in recipes, e.g. 'gin' or 'lime juice'.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {
            "type": "object",
            "required": ["name", "status"],
            "properties": {
              "name": {"type": "string", "description": "Ingredient name, e.g. 'gin'"},
              "status": {"type": "string", "enum": ["in_stock", "low", "out"], "description": "New stock status"}
            }
          }}}
        },
        "responses": {"200": {"description": "Updated entry", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/recipes/search": {
      "get": {
        "operationId": "searchRecipes",
        "summary": "Search cocktail recipes",
        "description": "Search the bar's recipe book. Each result reports whether it is makeable from current stock, which ingredients are missing (out of stock or untracked), and any known allergens. Set only_makeable=true when the guest wants something available right now.",
        "parameters": [
          {"name": "q", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Free-text query, e.g. 'fruity', 'gin sour', 'negroni'"},
          {"name": "only_makeable", "in": "query", "required": false, "schema": {"type": "boolean"}, "description": "If true, only return recipes makeable from current stock"},
          {"name": "max", "in": "query", "required": false, "schema": {"type": "integer", "default": 10}, "description": "Maximum number of recipes to return (capped at 25)"}
        ],
        "responses": {"200": {"description": "Matching recipes", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/recipes/{slug}": {
      "get": {
        "operationId": "getRecipe",
        "summary": "Get a full recipe",
        "description": "Returns the full recipe for a slug: ingredients with availability, preparation steps, tags and allergens, and whether it is makeable now.",
        "parameters": [{"name": "slug", "in": "path", "required": true, "schema": {"type": "string"}, "description": "Recipe slug from a search result"}],
        "responses": {"200": {"description": "Recipe detail", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/guests": {
      "get": {
        "operationId": "searchGuests",
        "summary": "Search or list guests",
        "description": "Find regular guests by name/handle or notes. Omit q to list everyone. Use this to recall a guest before recommending drinks.",
        "parameters": [{"name": "q", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Name or note fragment; omit to list all"}],
        "responses": {"200": {"description": "Matching guests", "content": {"application/json": {"schema": {"type": "object"}}}}}
      },
      "put": {
        "operationId": "upsertGuest",
        "summary": "Create or update a guest",
        "description": "Create a guest or update their free-text notes. Use a stable handle (e.g. the guest's name).",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {
            "type": "object",
            "required": ["handle"],
            "properties": {
              "handle": {"type": "string", "description": "Guest name/handle, e.g. 'Anna'"},
              "notes": {"type": "string", "description": "Free-text notes about the guest"}
            }
          }}}
        },
        "responses": {"200": {"description": "Saved guest", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/guests/get": {
      "get": {
        "operationId": "getGuest",
        "summary": "Get a guest's profile and preferences",
        "description": "Returns a guest's notes plus their likes, dislikes and allergies. ALWAYS check allergies against a recipe's allergens before recommending it.",
        "parameters": [{"name": "handle", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Guest name/handle"}],
        "responses": {"200": {"description": "Guest profile", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/guests/preferences": {
      "post": {
        "operationId": "addGuestPreference",
        "summary": "Record a guest preference",
        "description": "Add a like, dislike or allergy for a guest (creates the guest if new). Record allergies verbatim and conservatively.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {
            "type": "object",
            "required": ["handle", "kind", "value"],
            "properties": {
              "handle": {"type": "string", "description": "Guest name/handle"},
              "kind": {"type": "string", "enum": ["like", "dislike", "allergy"], "description": "Kind of preference"},
              "value": {"type": "string", "description": "What they like/dislike or are allergic to, e.g. 'mezcal', 'nuts'"}
            }
          }}}
        },
        "responses": {"200": {"description": "Recorded", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    }
  }
}`
