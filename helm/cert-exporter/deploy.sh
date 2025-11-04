#!/usr/bin/env bash


helm upgrade --install cert-exporter . -n monitoring --create-namespace --force