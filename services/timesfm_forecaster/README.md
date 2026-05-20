# TimesFM forecaster spike

This is the Python forecasting service for the ixr token-usage forecast spike.
It is intentionally decoupled from the Go API process.

The service exposes HTTP now and includes a gRPC proto contract for the
production direction.

## Run without TimesFM

This mode is useful for integration and orchestration testing.

```bash
cd services/timesfm_forecaster
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
uvicorn app:app --host 0.0.0.0 --port 8088
```

## Run with TimesFM

Install TimesFM and enable the backend:

```bash
pip install -r requirements.txt
git clone https://github.com/google-research/timesfm.git /tmp/timesfm
pip install -e '/tmp/timesfm[torch]'
TIMESFM_ENABLE=1 uvicorn app:app --host 0.0.0.0 --port 8088
```

The Go service should point at this process:

```bash
IXR_TIMESFM_URL=http://timesfm-forecaster:8088 IXR_TIMESFM_TIMEOUT_MS=20 ./ixr
```

For local testing, `http://localhost:8088` works only when both processes run
on the same machine. In production, use service discovery, Kubernetes DNS, or
an internal gateway name.

## Benchmark

```bash
python bench.py --url http://localhost:8088/v1/forecast --requests 100
```

The spike target is to measure whether raw inference can approach the 10-20 ms
budget. Expect the real TimesFM PyTorch path to need warmup, batching, and
possibly ONNX/TensorRT or managed inference to hit that budget consistently.
