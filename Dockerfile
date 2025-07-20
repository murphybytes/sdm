FROM golang:alpine AS build 
WORKDIR /app 
COPY . /app/ 
RUN go build -o server .

FROM scratch 
COPY --from=build /app/server /server
ENTRYPOINT ["/server"]
