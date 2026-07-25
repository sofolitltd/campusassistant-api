FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1001 appuser
WORKDIR /app
COPY campusassistant-api .
USER appuser
EXPOSE 8080
CMD ["./campusassistant-api"]
