-- +goose Up
-- Whether the worker binary that bootstrapped this connection sends its git
-- through the control plane's git proxy (worker capability git.proxy). It is
-- recorded once, by the bootstrap exchange, before any coding agent runs in the
-- sandbox, and heartbeats never change it, so an agent holding the worker
-- token cannot make its connection look like a pre-proxy one. Connections
-- bootstrapped before the proxy existed are false and keep receiving a push
-- token until their sandbox is recreated.
ALTER TABLE ao_worker_connections
    ADD COLUMN git_proxy BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE ao_worker_connections DROP COLUMN git_proxy;
