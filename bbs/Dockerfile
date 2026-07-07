FROM python:3.12-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends openssh-server tini \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt /tmp/bbs-requirements.txt
RUN pip install --no-cache-dir -r /tmp/bbs-requirements.txt \
    && rm /tmp/bbs-requirements.txt

RUN useradd --create-home --shell /bin/bash bbs \
    && mkdir -p /run/sshd /var/lib/bbs /home/bbs/.ssh \
    && chown -R bbs:bbs /var/lib/bbs /home/bbs/.ssh \
    && chmod 700 /home/bbs/.ssh

COPY sshd_config /etc/ssh/sshd_config
COPY bbs_app.py /usr/local/bin/bbs_app.py
COPY bbs_lib /usr/local/bin/bbs_lib
COPY translations.json /usr/local/bin/translations.json
COPY bbs_session.sh /usr/local/bin/bbs_session.sh
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod 755 /usr/local/bin/bbs_app.py /usr/local/bin/bbs_session.sh /usr/local/bin/docker-entrypoint.sh

EXPOSE 2222

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/sbin/sshd", "-D", "-e"]
