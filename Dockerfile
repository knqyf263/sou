FROM gcr.io/distroless/static-debian12:nonroot

ENV TERM=xterm-256color

COPY sou /usr/local/bin/

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/sou"] 