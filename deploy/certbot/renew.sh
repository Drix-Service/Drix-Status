#!/bin/sh
# Runs inside the certbot container. Gets a Let's Encrypt certificate for
# the status and admin domains, then renews it twice a day. Envoy forwards
# /.well-known/acme-challenge/ to this container on port 8888.
set -eu

if [ "${DRIX_TLS:-letsencrypt}" != "letsencrypt" ]; then
  echo "DRIX_TLS=${DRIX_TLS}: not using Let's Encrypt; idling."
  while :; do sleep 86400; done
fi

args=""
for d in $DOMAINS; do args="$args -d $d"; done
staging=""
force=""
if [ -n "${LE_STAGING:-}" ]; then
  staging="--test-cert"
elif grep -qs "acme-staging" /etc/letsencrypt/renewal/drix.conf; then
  # Staging was switched off: replace the test certificate now.
  echo "Replacing the staging certificate with a real one."
  force="--force-renewal"
fi

# Envoy needs a moment to come up before the first challenge.
sleep 5
until certbot certonly --standalone --http-01-port 8888 --non-interactive --agree-tos \
  -m "$LE_EMAIL" --cert-name drix $args $staging $force --keep-until-expiring \
  --deploy-hook /drix/deploy-hook.sh; do
  echo "Certificate request failed; retrying in 10 minutes (check DNS points here)."
  sleep 600
done

# certonly skips the hook when the existing certificate is still valid.
RENEWED_LINEAGE=/etc/letsencrypt/live/drix sh /drix/deploy-hook.sh

while :; do
  sleep 43200
  certbot renew --http-01-port 8888 --deploy-hook /drix/deploy-hook.sh || true
done
