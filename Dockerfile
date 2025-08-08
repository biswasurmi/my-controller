# ---------- Stage 1: Build ----------
FROM golang:1.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -o my-controller main.go

# ---------- Stage 2: Run ----------
FROM alpine:3.18

WORKDIR /app

COPY --from=builder /app/my-controller .

EXPOSE 8080

ENTRYPOINT ["./my-controller"]
