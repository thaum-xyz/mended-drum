# mended-drum

Tool server for the **Mended Drum** bar assistant — exposes the bar's
capabilities (inventory, recipes, guests) as an OpenAPI tool server consumed by
[Open WebUI](https://github.com/open-webui/open-webui). Recipes are backed by
[Mealie](https://mealie.io); model access goes through a self-hosted
[LiteLLM](https://github.com/BerriAI/litellm) gateway.

Deployment manifests live in
[`ankhmorpork/apps/mended-drum`](https://github.com/thaum-xyz/ankhmorpork/tree/master/apps/mended-drum).

## Status

Phase 0 scaffolding. Only health endpoints are served today; the tool surface
(`/inventory`, `/recipes`, `/guests`) lands in phase 1.

## Local development

```sh
make run            # serves on :8080 (override with PORT)
curl localhost:8080/healthz
make test           # go test ./...
make docker-build   # build the container image locally
```

## Layout

```
cmd/mended-drum     entrypoint
internal/server     HTTP routes
```
