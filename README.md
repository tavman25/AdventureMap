# AdventureMap 🌍

A beautiful, self-hosted travel map built with **Go + Leaflet.js**. Pin every place you've visited on a stunning dark-theme interactive world map.

## Features

- 🗺️ **Interactive world map** — Leaflet.js with CartoDB dark tiles & marker clustering
- 📍 **Add/edit/delete pins** — click anywhere on the map to drop a pin, or use the form
- ⭐ **Custom icons & colors** — choose from emoji icons and a full color picker
- 📂 **Google Maps import** — drag-and-drop your Google Takeout JSON to bulk-import saved places
- 🔍 **Search & filter** — sidebar search across all your pins
- 💾 **SQLite persistence** — zero-config, file-based database
- 🐳 **Docker-ready** — single container, multi-arch (amd64 + arm64)
- 🚀 **GitHub Actions CI/CD** — auto-build & push to GitHub Container Registry

## Quick Start

### Local (with Docker)
```bash
git clone https://github.com/YOUR_USERNAME/travel-map.git
cd travel-map
docker compose up --build
```
Open [http://localhost:8080](http://localhost:8080)

### Local (Go native — requires Go 1.23+)
```bash
git clone https://github.com/YOUR_USERNAME/travel-map.git
cd travel-map
go mod tidy
go run .
```
Open [http://localhost:8080](http://localhost:8080)

## Importing Google Maps Places

1. Go to [Google Takeout](https://takeout.google.com) → select **Maps (your places)**
2. Download and extract the archive
3. Open the app → click **Import** → drop `Saved Places.json` or paste the JSON
4. All your saved places appear as green ⭐ pins instantly

## Environment Variables

| Variable  | Default             | Description                     |
|-----------|---------------------|---------------------------------|
| `PORT`    | `8080`              | HTTP port to listen on          |
| `DB_PATH` | `./data/travel.db`  | Path to the SQLite database     |
| `GIN_MODE`| `release`           | Gin mode (`debug` / `release`)  |

## GitHub Actions / CI-CD

The workflow in `.github/workflows/ci-cd.yml`:

- **Every PR** → runs `go vet` + `go test`
- **Push to main** → builds a multi-arch Docker image and pushes to `ghcr.io/YOUR_USERNAME/travel-map:latest`

To deploy to a server automatically, uncomment the `deploy` job and add these secrets to your GitHub repo:

| Secret           | Description                    |
|------------------|--------------------------------|
| `DEPLOY_HOST`    | Server IP / hostname           |
| `DEPLOY_USER`    | SSH username                   |
| `DEPLOY_SSH_KEY` | Private SSH key (PEM format)   |
| `DEPLOY_PORT`    | SSH port (default `22`)        |

## Project Structure

```
travel-map/
├── main.go                     # Entry point, router setup
├── main_test.go                # Integration tests
├── go.mod / go.sum
├── Dockerfile
├── docker-compose.yml
├── .github/workflows/ci-cd.yml
├── internal/
│   ├── database/db.go          # SQLite operations
│   ├── handlers/handlers.go    # HTTP handlers
│   └── models/pin.go           # Data models
└── static/
    ├── index.html
    ├── css/app.css
    └── js/app.js
```

## REST API

| Method   | Endpoint                    | Description                  |
|----------|-----------------------------|------------------------------|
| `GET`    | `/api/pins`                 | List all pins                |
| `GET`    | `/api/pins/:id`             | Get a single pin             |
| `POST`   | `/api/pins`                 | Create a pin                 |
| `PUT`    | `/api/pins/:id`             | Update a pin                 |
| `DELETE` | `/api/pins/:id`             | Delete a pin                 |
| `POST`   | `/api/import/googlemaps`    | Bulk import from Google JSON |
