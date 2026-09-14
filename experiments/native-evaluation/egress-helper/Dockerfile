FROM node@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0
USER 0:0
WORKDIR /opt/gaffer-egress-helper
COPY manifest.json verify-dependencies.ts extract-payloads.ts ./
COPY metadata/ ./metadata/
COPY packages/ ./packages/
RUN --network=none node ./extract-payloads.ts
USER 1000:1000
ENV HOME=/nonexistent NODE_OPTIONS=
ENTRYPOINT ["/usr/sbin/nft"]
CMD ["--version"]
