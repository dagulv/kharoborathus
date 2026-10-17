FROM node:24-alpine AS base
RUN corepack enable
WORKDIR /app
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
# The lockfile pins tarballs to a local registry proxy. Integrity hashes still verify the npm copies.
RUN sed -i 's#http://127.0.0.1:11230/node/#https://registry.npmjs.org/#g' pnpm-lock.yaml

FROM base AS deps
RUN pnpm install --frozen-lockfile --prod

FROM base AS build
RUN pnpm install --frozen-lockfile
COPY tsconfig.json vite.config.ts ./
COPY src src
COPY static static
# Route analysis validates env at build time. The real value is read at runtime.
RUN POSTGRES_CONNECTION_STRING=postgresql://build@localhost/build pnpm build

FROM node:24-alpine
WORKDIR /app
ENV NODE_ENV=production
COPY package.json ./
COPY --from=deps /app/node_modules node_modules
COPY --from=build /app/build build
USER node
EXPOSE 3000
CMD ["node", "build"]
