# Usage forecasting

ixr records successful chat-completion token usage by user and can forecast near-term
token consumption.

## Recording usage

Send a stable user identifier with requests:

```http
X-IXR-User: user-123
```

The chat handler emits that value on `CallEvent.UserID`. The usage forecast
recorder subscribes to the existing event bus and stores successful calls in the
in-memory usage store.

## Forecast API

```http
GET /v1/usage/forecast?user_id=user-123&model=gpt-4o&free_token_limit=100000&horizon_hours=24&bucket_minutes=60
```

Query parameters:

| name | required | default | description |
| --- | --- | --- | --- |
| `user_id` | no | `X-IXR-User` header | User to forecast. Required through either query or header. |
| `model` | no | all models | Optional model filter. |
| `free_token_limit` | no | `0` | Free-token allowance to compare against observed + projected usage. |
| `window_hours` | no | `168` | Historical lookback window. |
| `horizon_hours` | no | `24` | Forecast horizon. |
| `bucket_minutes` | no | `60` | History and forecast bucket size. |

Response fields include:

| field | description |
| --- | --- |
| `consumed_tokens` | Tokens already consumed in the lookback window. |
| `current_rate_tokens_per_hour` | Average token usage over the lookback window. |
| `projected_tokens` | Forecast tokens over the horizon. |
| `free_tokens_remaining` | Remaining allowance after current usage. |
| `projected_over_limit` | Whether current + forecast usage exceeds `free_token_limit`. |
| `forecast` | Timestamped forecast buckets. |

## TimesFM backend

The Go service keeps forecasting behind a `usageforecast.Forecaster` interface.
When `IXR_TIMESFM_URL` or `forecast.timesfm_url` is set, ixr uses an HTTP
TimesFM sidecar as the primary forecaster and falls back to a local moving
average if the sidecar fails.

Expected sidecar contract:

```http
POST /forecast
Content-Type: application/json

{"horizon":24,"inputs":[[120,80,160,90]]}
```

```json
{"point_forecast":[[100,110,105]]}
```

Google Research's current TimesFM README documents TimesFM 2.5, an open
time-series forecasting model with up to 16k context length and a Python example
using `timesfm.TimesFM_2p5_200M_torch.from_pretrained("google/timesfm-2.5-200m-pytorch")`.
