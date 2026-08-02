FROM node:22-alpine
WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev
COPY server.mjs ./
ENV PULSE_MCP_PORT=8002
EXPOSE 8002
CMD ["node", "server.mjs"]
