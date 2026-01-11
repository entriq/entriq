FROM golang:1.25-alpine AS build

RUN apk add --no-cache go make

WORKDIR /app

COPY . .

RUN make dist

FROM scratch
COPY --from=build /app/app /bin/app
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
EXPOSE 8080/tcp
CMD [ "/bin/app" ]