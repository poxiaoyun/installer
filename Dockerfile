FROM alpine
# TARGETOS TARGETARCH already set by '--platform'
ARG TARGETOS TARGETARCH
COPY ${TARGETOS}-${TARGETARCH}/ /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/installer"]