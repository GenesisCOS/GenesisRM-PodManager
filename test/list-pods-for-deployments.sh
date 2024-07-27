#!/bin/bash

curl -X POST http://localhost:10000/api/v1/deployments/pods -H 'content-type: application/json' \
    -d '{"metadatas": [{"name": "travel-dep", "namespace": "train-ticket-jaeger"}, {"name": "travel-plan-dep", "namespace": "train-ticket-jaeger"}]}'\
    > /dev/null 
