FROM golang:1.22-alpine AS build

WORKDIR /src
RUN apk add --no-cache build-base
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/bbs_app ./cmd/bbs

FROM alpine:3.20

RUN apk add --no-cache bash ca-certificates fail2ban iproute2 iptables openssh-server sqlite tini

RUN addgroup -S bbs \
    && adduser -S -D -h /home/bbs -s /bin/bash -G bbs bbs \
    && mkdir -p /run/sshd /var/lib/bbs /home/bbs/.ssh \
    && chown -R bbs:bbs /var/lib/bbs /home/bbs/.ssh \
    && chmod 700 /home/bbs/.ssh

COPY sshd_config /etc/ssh/sshd_config
COPY fail2ban/fail2ban.local /etc/fail2ban/fail2ban.local
COPY fail2ban/jail.local /etc/fail2ban/jail.local
COPY --from=build /out/bbs_app /usr/local/bin/bbs_app
COPY translations.json /usr/local/bin/translations.json
COPY bbs_session.sh /usr/local/bin/bbs_session.sh
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod 755 /usr/local/bin/bbs_app /usr/local/bin/bbs_session.sh /usr/local/bin/docker-entrypoint.sh

EXPOSE 2222

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/sbin/sshd", "-D", "-E", "/var/log/auth.log"]
