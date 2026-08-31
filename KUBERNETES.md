# Kubernetes Deployment

This guide covers deploying `buildkite-mcp-server` as a sidecar in Kubernetes.

## Container Images

Images are published to:
- `ghcr.io/buildkite/buildkite-mcp-server`
- `docker.io/buildkite/mcp-server`

Built on `cgr.dev/chainguard/static:latest` base image.

## Sidecar Deployment

The HTTP mode is designed for sidecar deployments where your application needs access to Buildkite APIs via MCP.

### Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `BUILDKITE_API_TOKEN` | required | Buildkite API token |
| `HTTP_LISTEN_ADDR` | `localhost:3000` | Address to bind |
| `BUILDKITE_TOOLSETS` | `all` | Comma-separated toolsets or `all` |
| `BUILDKITE_READ_ONLY` | `false` | Disable write operations |

### Endpoints

| Path | Description |
|------|-------------|
| `/mcp` | Streamable HTTP endpoint (default) |
| `/sse` | SSE endpoint (when `--use-sse` is set) |
| `/health` | Health check endpoint |

### Example Deployment

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: buildkite-mcp-secrets
type: Opaque
stringData:
  api-token: "bkua_xxxxxxxxxxxxxxxx"
  mcp-auth-token: "your-shared-auth-token"  # optional
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
        # Your main application
        - name: app
          image: my-app:latest
          env:
            - name: MCP_SERVER_URL
              value: "http://localhost:3000/mcp"
            - name: MCP_AUTH_TOKEN
              valueFrom:
                secretKeyRef:
                  name: buildkite-mcp-secrets
                  key: mcp-auth-token

        # Buildkite MCP sidecar
        - name: buildkite-mcp
          image: ghcr.io/buildkite/buildkite-mcp-server:latest
          args:
            - http
            - --listen=0.0.0.0:3000
          env:
            - name: BUILDKITE_API_TOKEN
              valueFrom:
                secretKeyRef:
                  name: buildkite-mcp-secrets
                  key: api-token
            - name: BUILDKITE_READ_ONLY
              value: "true"  # recommended for sidecars
            - name: BUILDKITE_TOOLSETS
              value: "all"   # or specific: "pipelines,builds"
          ports:
            - containerPort: 3000
              name: mcp
          livenessProbe:
            httpGet:
              path: /health
              port: 3000
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /health
              port: 3000
            initialDelaySeconds: 2
            periodSeconds: 5
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "128Mi"
              cpu: "200m"
```

### Connecting from Your Application

Since both containers share the same pod network, your application connects via localhost:

```
http://localhost:3000/mcp
```

### OpenTelemetry Tracing

To enable OTEL tracing, add the exporter configuration:

```yaml
env:
  - name: OTEL_EXPORTER_OTLP_PROTOCOL
    value: "grpc"  # or "http/protobuf"
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://otel-collector:4317"
```

### Standalone Service

If you prefer a shared MCP server instead of per-pod sidecars:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: buildkite-mcp
spec:
  selector:
    app: buildkite-mcp
  ports:
    - port: 3000
      targetPort: 3000
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: buildkite-mcp
spec:
  replicas: 2
  selector:
    matchLabels:
      app: buildkite-mcp
  template:
    metadata:
      labels:
        app: buildkite-mcp
    spec:
      containers:
        - name: buildkite-mcp
          image: ghcr.io/buildkite/buildkite-mcp-server:latest
          args:
            - http
            - --listen=0.0.0.0:3000
          env:
            - name: BUILDKITE_API_TOKEN
              valueFrom:
                secretKeyRef:
                  name: buildkite-mcp-secrets
                  key: api-token
            - name: BUILDKITE_READ_ONLY
              value: "true"
          ports:
            - containerPort: 3000
          livenessProbe:
            httpGet:
              path: /health
              port: 3000
          readinessProbe:
            httpGet:
              path: /health
              port: 3000
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "256Mi"
              cpu: "500m"
```

Applications connect via the service: `http://buildkite-mcp:3000/mcp`
