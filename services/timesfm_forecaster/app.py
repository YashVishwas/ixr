import time

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from backend import ForecastBackend, load_backend


class ForecastRequest(BaseModel):
    horizon: int = Field(gt=0, le=4096)
    inputs: list[list[float]] = Field(min_length=1)


class ForecastResponse(BaseModel):
    point_forecast: list[list[float]]
    latency_ms: float
    backend: str


app = FastAPI(title="ixr TimesFM Forecaster", version="0.1.0")
backend: ForecastBackend | None = None


@app.on_event("startup")
def startup() -> None:
    global backend
    backend = load_backend()


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok", "backend": backend.name if backend else "not_ready"}


@app.post("/v1/forecast", response_model=ForecastResponse)
def forecast(req: ForecastRequest) -> ForecastResponse:
    if backend is None:
        raise HTTPException(status_code=503, detail="forecast backend is not ready")
    start = time.perf_counter()
    try:
        points = backend.forecast(req.inputs, req.horizon)
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc)) from exc
    latency_ms = (time.perf_counter() - start) * 1000
    return ForecastResponse(
        point_forecast=points,
        latency_ms=latency_ms,
        backend=backend.name,
    )
