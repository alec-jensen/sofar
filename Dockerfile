FROM node:24-alpine AS web
WORKDIR /src
COPY package*.json ./
RUN npm ci
COPY index.html tsconfig.json vite.config.ts ./
COPY src ./src
# the app and the server share one merchant directory
COPY internal/budget/merchants.json ./internal/budget/merchants.json
COPY public ./public
RUN npm run build

FROM golang:1.26-alpine AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY server ./server
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sofar ./server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 sofar
WORKDIR /app
COPY --from=api /sofar /app/sofar
COPY --from=web /src/dist /app/dist
USER sofar
ENV SOFAR_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/app/sofar"]
