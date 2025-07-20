FROM --platform=$BUILDPLATFORM golang:alpine AS build 
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app 
COPY . /app/ 
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o server .

FROM scratch 
COPY --from=build /app/server /server
ENTRYPOINT ["/server"]
