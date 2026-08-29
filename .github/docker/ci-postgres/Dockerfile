# CI Postgres image: PostgreSQL 19beta2 + pgvector built from a pinned master
# commit. PG 19 support landed on pgvector master (upstream issue closed
# 2026-07-29) but is not yet in a tagged release, so we build from source.
#
# Version policy (docs/memory-pg/01 — "Postgres version policy"): dev + CI run
# 19beta2 so every memory-program PR is proven against 19 before its GA;
# prod cutover targets 19-GA-if-released-else-18. The self-host compose default
# is pgvector/pgvector:pg16 (same major as before, volume-compatible, extension
# ready for migration 057); its major bump ships with the prod cutover step.
#
# Bump PGVECTOR_REF deliberately — it pins the build for reproducibility.
FROM postgres:19beta2-bookworm

ARG PGVECTOR_REF=4f3d17f6f74fe98adf54df4d016de241eeaae9af

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
         build-essential git postgresql-server-dev-19 ca-certificates \
    && git clone https://github.com/pgvector/pgvector.git /tmp/pgvector \
    && cd /tmp/pgvector \
    && git checkout "$PGVECTOR_REF" \
    && make \
    && make install \
    && apt-get purge -y build-essential git postgresql-server-dev-19 \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* /tmp/pgvector
