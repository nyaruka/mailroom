FROM "mcr.microsoft.com/devcontainers/go:dev-1.25-trixie"

# Install PostgreSQL client 15
RUN apt-get update \
    && apt-get install -y wget gnupg2 lsb-release \
    && echo "deb http://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" > /etc/apt/sources.list.d/pgdg.list \
    && wget --quiet -O - https://www.postgresql.org/media/keys/ACCC4CF8.asc | tee /etc/apt/trusted.gpg.d/postgresql.asc > /dev/null \
    && apt-get update \
    && apt-get install -y --no-install-recommends postgresql-client-17 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
