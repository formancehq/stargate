VERSION 0.8

ARG core=github.com/formancehq/earthly:main
IMPORT $core AS core

FROM core+base-image

deploy-staging:
    BUILD --pass-args core+deploy-staging