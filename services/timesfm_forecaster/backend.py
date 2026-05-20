import os
import time
from typing import Protocol


class ForecastBackend(Protocol):
    name: str

    def forecast(self, inputs: list[list[float]], horizon: int) -> list[list[float]]:
        ...


class MovingAverageBackend:
    name = "moving_average"

    def __init__(self, lookback: int = 24):
        self.lookback = lookback

    def forecast(self, inputs: list[list[float]], horizon: int) -> list[list[float]]:
        output: list[list[float]] = []
        for series in inputs:
            window = series[-self.lookback :] if series else []
            avg = sum(window) / len(window) if window else 0.0
            output.append([max(0.0, avg)] * horizon)
        return output


class TimesFMBackend:
    name = "timesfm"

    def __init__(self) -> None:
        model_id = os.getenv("TIMESFM_MODEL_ID", "google/timesfm-2.5-200m-pytorch")
        import timesfm

        self.model = timesfm.TimesFM_2p5_200M_torch.from_pretrained(model_id)
        self.model.compile(
            timesfm.ForecastConfig(
                max_context=int(os.getenv("TIMESFM_MAX_CONTEXT", "1024")),
                max_horizon=int(os.getenv("TIMESFM_MAX_HORIZON", "256")),
                normalize_inputs=True,
                use_continuous_quantile_head=True,
                force_flip_invariance=True,
                infer_is_positive=True,
                fix_quantile_crossing=True,
            )
        )

    def forecast(self, inputs: list[list[float]], horizon: int) -> list[list[float]]:
        import numpy as np

        raw = self.model.forecast(
            horizon=horizon,
            inputs=[np.asarray(series, dtype=float) for series in inputs],
        )
        return normalize_timesfm_output(raw, horizon)


def normalize_timesfm_output(raw: object, horizon: int) -> list[list[float]]:
    if isinstance(raw, tuple):
        raw = raw[0]
    if hasattr(raw, "tolist"):
        raw = raw.tolist()
    if not isinstance(raw, list):
        raise ValueError("TimesFM returned an unsupported forecast shape")

    output: list[list[float]] = []
    for row in raw:
        if hasattr(row, "tolist"):
            row = row.tolist()
        values = list(row)
        if values and isinstance(values[0], list):
            values = values[0]
        output.append([float(v) for v in values[:horizon]])
    return output


def load_backend() -> ForecastBackend:
    if os.getenv("TIMESFM_ENABLE") != "1":
        return MovingAverageBackend()
    start = time.perf_counter()
    backend = TimesFMBackend()
    elapsed_ms = (time.perf_counter() - start) * 1000
    print(f"loaded TimesFM backend in {elapsed_ms:.2f} ms", flush=True)
    return backend
