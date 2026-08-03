# The binary is built by `make docker/build` (which runs `make build/linux`),
# not here: this image stages a prebuilt artefact rather than carrying a Go
# toolchain and a module cache into the build.
ARG ALPINE_TAG=3.22

FROM alpine:${ALPINE_TAG} AS builder

# scratch has no /etc/passwd, so a numeric USER in the final stage would run
# as a uid with no name behind it. These two files are the whole of what it
# takes to have one.
RUN printf 'nobody:x:65534:65534:nobody:/home/app:/sbin/nologin\n' > /tmp/passwd \
	&& printf 'nobody:x:65534:\n' > /tmp/group \
	&& apk add --no-cache ca-certificates \
	&& mkdir -p /tmp/home/app

COPY bin/deskline-linux-amd64 /tmp/app

FROM scratch

COPY --from=builder /tmp/passwd /tmp/group /etc/
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder --chown=65534:65534 /tmp/home/app /home/app
COPY --from=builder /tmp/app /bin/app

USER nobody:nobody
WORKDIR /home/app

EXPOSE 8080

ENTRYPOINT ["/bin/app"]
CMD ["-addr", "0.0.0.0:8080"]
