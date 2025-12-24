#!/usr/bin/env bash


docker run --name sentinel-prometheus-1 -p 9090:9090 \
  -v $(pwd)/prometheus.yaml:/etc/prometheus/prometheus.yml \
  prom/prometheus
