import argparse
import statistics
import time

import httpx


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://localhost:8088/v1/forecast")
    parser.add_argument("--requests", type=int, default=100)
    parser.add_argument("--horizon", type=int, default=24)
    parser.add_argument("--context", type=int, default=168)
    args = parser.parse_args()

    payload = {
        "horizon": args.horizon,
        "inputs": [[float((i % 24) * 10 + 100) for i in range(args.context)]],
    }
    latencies: list[float] = []

    with httpx.Client(timeout=30.0) as client:
        for _ in range(args.requests):
            start = time.perf_counter()
            resp = client.post(args.url, json=payload)
            resp.raise_for_status()
            latencies.append((time.perf_counter() - start) * 1000)

    latencies.sort()
    print(f"requests={args.requests}")
    print(f"min_ms={latencies[0]:.2f}")
    print(f"p50_ms={statistics.median(latencies):.2f}")
    print(f"p95_ms={latencies[int(len(latencies) * 0.95) - 1]:.2f}")
    print(f"max_ms={latencies[-1]:.2f}")


if __name__ == "__main__":
    main()
