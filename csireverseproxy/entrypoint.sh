#!/bin/bash

# Define the directory and file paths
TLS_DIR="/app/tls"
TLS_CRT="$TLS_DIR/tls.crt"
TLS_KEY="$TLS_DIR/tls.key"
OPENSSL_CNF="$TLS_DIR/openssl.cnf"

# Check if the TLS certificate and key exist
if [ ! -f "$TLS_CRT" ] || [ ! -f "$TLS_KEY" ]; then
  echo "Generating TLS certificate and key..."

  # Generate the TLS key
  openssl genrsa -out "$TLS_KEY" 2048

  # Generate the certificate signing request (CSR)
  openssl req -new -key "$TLS_KEY" -out "$TLS_DIR/tls.csr" -config "$OPENSSL_CNF"

  # Generate the TLS certificate
  openssl x509 -req -in "$TLS_DIR/tls.csr" -signkey "$TLS_KEY" -out "$TLS_CRT" -days 3650 -extensions req_ext -extfile "$OPENSSL_CNF"

  echo "TLS certificate and key generated."
else
  echo "TLS certificate and key already exist."
fi

# Execute the CMD from the Dockerfile
exec "$@"
