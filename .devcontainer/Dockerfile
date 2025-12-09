FROM "mcr.microsoft.com/devcontainers/go:dev-1.25-trixie"

RUN apt-get update && apt-get install -y binutils curl gnupg

# install postgres 15 client
RUN echo "deb http://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" > /etc/apt/sources.list.d/pgdg.list \
    && curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /etc/apt/trusted.gpg.d/postgresql.gpg \
    && apt-get update \
    && apt-get install -y postgresql-client-15
