FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/tailscale-x/tailscale-private-funnel" \
      org.opencontainers.image.title="tailscale-private-funnel" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.description="A self-hosted, policy-led public edge for services that stay on your Tailscale tailnet."
COPY --chmod=0555 dist/tailscale-private-funnel-${TARGETOS}-${TARGETARCH} /usr/local/bin/tailscale-private-funnel
ENV HOME=/data XDG_DATA_HOME=/data XDG_CONFIG_HOME=/data/config
ENTRYPOINT ["/usr/local/bin/tailscale-private-funnel"]
