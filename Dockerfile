FROM golang:1.24 AS builder

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /nexa-scheduler ./cmd/scheduler

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /nexa-scheduler /nexa-scheduler

USER 65532:65532

ENTRYPOINT ["/nexa-scheduler"]
