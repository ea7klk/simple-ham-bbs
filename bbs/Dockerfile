FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
RUN go build -trimpath -ldflags="-s -w" -o /out/bbs_app ./cmd/bbs

FROM python:3.12-slim AS aprsd

RUN apt-get update \
    && apt-get install -y --no-install-recommends git \
    && rm -rf /var/lib/apt/lists/*

RUN python -m venv /opt/aprsd \
    && /opt/aprsd/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/aprsd/bin/pip install --no-cache-dir git+https://github.com/craigerl/aprsd.git

FROM python:3.12-slim

ENV APRSD_BIN=/opt/aprsd/bin/aprsd
ENV PATH="/opt/aprsd/bin:${PATH}"

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates iproute2 openssh-server tini \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --shell /bin/bash bbs \
    && mkdir -p /run/sshd /var/lib/bbs /home/bbs/.ssh \
    && chown -R bbs:bbs /var/lib/bbs /home/bbs/.ssh \
    && chmod 700 /home/bbs/.ssh

COPY sshd_config /etc/ssh/sshd_config
COPY --from=build /out/bbs_app /usr/local/bin/bbs_app
COPY --from=aprsd /opt/aprsd /opt/aprsd
COPY translations.json /usr/local/bin/translations.json
COPY bbs_session.sh /usr/local/bin/bbs_session.sh
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod 755 /usr/local/bin/bbs_app /usr/local/bin/bbs_session.sh /usr/local/bin/docker-entrypoint.sh

EXPOSE 2222

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/sbin/sshd", "-D", "-e"]
