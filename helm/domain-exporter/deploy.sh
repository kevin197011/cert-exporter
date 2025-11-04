#!/usr/bin/env bash


helm upgrade --install domain-exporter . -n monitoring --create-namespace --force