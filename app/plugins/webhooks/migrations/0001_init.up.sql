CREATE TABLE plg_webhooks_endpoints (
  id          bigserial   PRIMARY KEY,
  url         text        NOT NULL,
  description text        NOT NULL DEFAULT '',
  events      text[]      NOT NULL,
  enabled     boolean     NOT NULL DEFAULT true,
  secret_enc  bytea       NOT NULL,
  secret_hint text        NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- payload is json, not jsonb, so the body sent (and signed) keeps its exact bytes across retries
CREATE TABLE plg_webhooks_deliveries (
  id               bigserial   PRIMARY KEY,
  endpoint_id      bigint      NOT NULL REFERENCES plg_webhooks_endpoints (id) ON DELETE CASCADE,
  event_id         uuid        NOT NULL,
  event            text        NOT NULL,
  payload          json        NOT NULL,
  status           text        NOT NULL DEFAULT 'pending'
                               CHECK (status IN ('pending', 'succeeded', 'failed', 'cancelled')),
  attempts         int         NOT NULL DEFAULT 0,
  last_status_code int,
  last_error       text,
  response_snippet text,
  duration_ms      int,
  redelivery_of    bigint,
  created_at       timestamptz NOT NULL DEFAULT now(),
  completed_at     timestamptz
);
CREATE UNIQUE INDEX plg_webhooks_deliveries_event_idx ON plg_webhooks_deliveries (endpoint_id, event_id)
  WHERE redelivery_of IS NULL;
CREATE INDEX plg_webhooks_deliveries_endpoint_idx ON plg_webhooks_deliveries (endpoint_id, created_at DESC);
CREATE INDEX plg_webhooks_deliveries_created_idx ON plg_webhooks_deliveries (created_at);
