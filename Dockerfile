FROM alpine:latest
WORKDIR /root/
COPY campusassistant-api .
EXPOSE 8080
CMD ["./campusassistant-api"]
