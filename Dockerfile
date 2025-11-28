FROM alpine
COPY bin/ /usr/local/bin/
WORKDIR /
ENTRYPOINT ["/usr/local/bin/installer"]