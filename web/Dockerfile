# Serve the static frontend and proxy API and Swagger requests.
FROM nginx:1.29-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
# The frontend does not require a JavaScript build step.
COPY static/ /usr/share/nginx/html/
