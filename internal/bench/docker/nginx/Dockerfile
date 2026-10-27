# nginx with the benchmark's configuration baked in, so the server stack
# runs the same on a remote Docker host (where bind mounts would name the
# remote host's files). 1.31 is built with http_v3_module and OpenSSL 3.5.
FROM nginx:1.31.8

COPY nginx.conf /etc/nginx/nginx.conf
COPY bench-locations.conf bench-headers.conf /etc/nginx/
COPY bench.js /etc/nginx/njs/bench.js
