FROM node:24-bookworm@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0
WORKDIR /opt/router-deps
COPY upstream-package.json ./package.json
COPY upstream-dependencies.lock.json ./package-lock.json
RUN npm ci --ignore-scripts --omit=optional --no-audit --no-fund
USER 1000:1000
WORKDIR /work
ENV HOME=/work/home NODE_OPTIONS=
