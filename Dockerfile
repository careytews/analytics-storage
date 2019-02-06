FROM fedora:26

RUN dnf install -y libgo

COPY storage /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/storage"]
CMD [ "/queue/input" ]


