FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/funil ./cmd/funil \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockresgata ./cmd/mockresgata \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/funil /app/funil
COPY --from=build /out/mockresgata /app/mockresgata
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV APP_ADDR=:8080 DATABASE_PATH=/data/funil.db
EXPOSE 8080
VOLUME ["/data"]
USER nonroot:nonroot
ENTRYPOINT ["/app/funil", "--env-file", "/dev/null"]
