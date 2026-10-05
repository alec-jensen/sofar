FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/index.html web/tsconfig.json web/vite.config.ts ./
COPY web/src ./src
COPY web/public ./public
# the app and the server share one merchant directory
COPY internal/normalize/merchants.json /src/internal/normalize/merchants.json
RUN npm run build

FROM golang:1.26-alpine AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sofar ./cmd/sofar

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 sofar
WORKDIR /app
COPY --from=api /sofar /app/sofar
COPY --from=web /src/web/dist /app/dist
USER sofar
ENV SOFAR_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/app/sofar"]
CMD ["serve"]
