# Usage forecasting spike

ixr records successful chat-completion token usage by user and can forecast
near-term consumption. The Go API stays independent of the Python TimesFM
runtime.

## Architecture

The spike uses three pieces:

| component | runtime | role |
| --- | --- | --- |
| Main API | Go | Records token usage and serves forecast APIs. |
| Forecast service | Python | Runs TimesFM behind HTTP today; gRPC proto is included for production. |
| Orchestrator | Go in-memory spike | Queues forecast jobs. Replace this with Temporal or another durable orchestrator later. |

The Go API never imports PyTorch or TimesFM. If `IXR_TIMESFM_URL` is set, Go
calls the remote Python service with a short timeout. If the call fails or
exceeds the timeout, Go falls back to a native moving-average forecast.

## Recording usage

Send a stable user identifier with chat requests:

```http
X-IXR-User: user-123
```

The chat handler emits that value on `CallEvent.UserID`. The usage forecast
recorder subscribes to the existing event bus and stores successful calls in the
in-memory usage store.

## Immediate forecast API

```http
GET /v1/usage/forecast?user_id=user-123&model=gpt-4o&free_token_limit=100000&horizon_hours=24&bucket_minutes=60
```

This is useful for local development and simple clients. It may call TimesFM,
but it will always return using the Go fallback if the remote service fails.

## Async job API

Create a forecast job:

```http
POST /v1/usage/forecast/jobs
Content-Type: application/json

{
  "user_id": "user-123",
  "model": "gpt-4o",
  "window_hours": 168,
  "horizon_hours": 24,
  "bucket_minutes": 60,
  "free_token_limit": 100000
}
```

Poll the job:

```http
GET /v1/usage/forecast/jobs/fcst_abc123
```

The spike uses an in-process queue and memory store. The contract is shaped so
the worker can later be backed by Temporal without changing client requests.

## Python TimesFM service

The standalone service lives in `services/timesfm_forecaster`.

```bash
cd services/timesfm_forecaster
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
uvicorn app:app --host 0.0.0.0 --port 8088
```

Run ixr against it:

```bash
IXR_TIMESFM_URL=http://localhost:8088 IXR_TIMESFM_TIMEOUT_MS=20 ./ixr
```

`localhost` is only for local development. In production, `IXR_TIMESFM_URL`
should be a service-discovery address such as
`http://timesfm-forecaster.default.svc.cluster.local:8088`.

To attempt the real TimesFM backend:

```bash
git clone https://github.com/google-research/timesfm.git /tmp/timesfm
pip install -e '/tmp/timesfm[torch]'
TIMESFM_ENABLE=1 uvicorn app:app --host 0.0.0.0 --port 8088
```

## Forecast service HTTP contract

```http
POST /v1/forecast
Content-Type: application/json

{"horizon":24,"inputs":[[120,80,160,90]]}
```

```json
{"point_forecast":[[100,110,105]],"latency_ms":12.4,"backend":"timesfm"}
```

The gRPC proto for the same contract is in
`services/timesfm_forecaster/proto/forecast.proto`.

## Benchmark

```bash
python services/timesfm_forecaster/bench.py \
  --url http://localhost:8088/v1/forecast \
  --requests 100
```

The 10-20 ms target is a spike hypothesis, not an assumption. Expect the real
PyTorch path to need warmup, batching, ONNX/TensorRT, or managed inference to
hit that target consistently.
