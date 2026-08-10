# postgres:18.4-trixie, Postgres 18.4-1.pgdg13+1 from PGDG, Debian 13 (trixie), Python 3.13.
FROM postgres@sha256:a02db8cac496f15b094798a38254f14d6e00741f709360e5e00bb6668ea31636

# These defaults serve poc/image/test/run-contract-tests.sh; deployed values
# come from the ConfigMap in poc/manifests/cluster/20-config.yaml.
ENV PATH=/opt/cnpatroni/venv/bin:$PATH \
    PGDATA=/var/lib/postgresql/pgdata \
    CNPATRONI_CONFIG_FILE=/etc/cnpatroni/patroni.yml \
    CNPATRONI_SECRETS_DIR=/etc/cnpatroni/secrets

RUN apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends python3 python3-venv \
 && python3 -m venv /opt/cnpatroni/venv \
 && rm -rf /var/lib/apt/lists/*

COPY requirements.txt /tmp/requirements.txt

RUN /opt/cnpatroni/venv/bin/pip install --no-cache-dir --no-deps -r /tmp/requirements.txt \
 && rm -f /tmp/requirements.txt

# Python 3.14 changes Linux multiprocessing's default to forkserver, altering Patroni's PID 1 topology.
RUN /opt/cnpatroni/venv/bin/python3 -c "import sys; assert sys.version_info < (3, 14), sys.version" \
 && /opt/cnpatroni/venv/bin/python3 -c "import psycopg, yaml, urllib3" \
 && /opt/cnpatroni/venv/bin/patroni --version | grep -Fx 'patroni 4.1.4'

COPY --chmod=0755 bin/cnpatroni-render-config /usr/local/bin/cnpatroni-render-config
COPY --chmod=0755 bin/cnpatroni-post-bootstrap /usr/local/bin/cnpatroni-post-bootstrap
COPY --chmod=0755 entrypoint.sh /usr/local/bin/cnpatroni-entrypoint

RUN chown -R root:root /opt/cnpatroni/venv \
 && chmod 0755 /opt/cnpatroni/venv \
 && mkdir -p /etc/cnpatroni/secrets /var/lib/postgresql/pgdata \
 && chown -R 999:999 /etc/cnpatroni /var/lib/postgresql/pgdata \
 && chmod 0700 /etc/cnpatroni /etc/cnpatroni/secrets /var/lib/postgresql/pgdata

EXPOSE 5432 8008

# Patroni's child handles SIGTERM explicitly; the inherited SIGINT stop signal bypasses that path.
STOPSIGNAL SIGTERM

USER 999:999

ENTRYPOINT ["/usr/local/bin/cnpatroni-entrypoint"]
