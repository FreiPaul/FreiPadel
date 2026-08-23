# A sqlite3 shell that shares the app's data volume.
#
# The tests must read the database from inside the Docker filesystem: SQLite's
# WAL index lives in shared memory backed by the -shm file, and that is not
# coherent across a macOS bind mount (virtiofs/gRPC-FUSE). A reader on the host
# sees a frozen snapshot and, on close, checkpoints the WAL it cannot see —
# silently discarding committed rows. Inside the volume there is one real
# filesystem and normal multi-process SQLite semantics apply.
FROM alpine:3
RUN apk add --no-cache sqlite
ENTRYPOINT ["sleep"]
CMD ["infinity"]
