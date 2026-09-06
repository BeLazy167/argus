# CI Postgres image: the official pgvector image at the exact versions
# production runs (Fly argus-db: PostgreSQL 17 + pgvector 0.8.5), pinned by
# digest so a moved tag cannot change what CI tests against.
#
# History: CI previously ran PostgreSQL 19beta2 with pgvector built from a
# pinned master commit, under a policy of proving every PR against 19 before
# its GA. That combination crashed the server mid-`CREATE INDEX ... USING hnsw`
# in migration 057 on roughly half of all runs (`driver: bad connection` with
# no SQL error — a backend crash, not a query failure), while production never
# left 17. CI now matches production; a forward-compat job against a newer
# major can be added separately when there is a GA release to target.
#
# Bump the tag and digest together, deliberately. The self-host compose default
# (pgvector/pgvector:pg16) is a separate concern and unchanged.
FROM pgvector/pgvector:0.8.5-pg17@sha256:d2ef61f42ef767baa5a1475393303cc235bcd92febd9d7014eddb48b41f3bad0
