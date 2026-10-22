# Image with the ceph/s3-tests suite at the commit given by S3TESTS_REF.
# Building it needs network access to github.com (the source archive) and to
# PyPI (the Python packages named in requirements.txt of that commit).
FROM python:3.12-slim

ARG S3TESTS_REF=5b3987116eca0b4b8fa57d6b10f3db57bab196b6

ADD https://github.com/ceph/s3-tests/archive/${S3TESTS_REF}.tar.gz /tmp/s3-tests.tar.gz
RUN mkdir -p /opt/s3-tests \
 && tar -xzf /tmp/s3-tests.tar.gz -C /opt/s3-tests --strip-components=1 \
 && rm -f /tmp/s3-tests.tar.gz \
 && pip install --no-cache-dir -r /opt/s3-tests/requirements.txt

COPY run-subset.sh /usr/local/bin/run-subset
RUN chmod 0755 /usr/local/bin/run-subset

WORKDIR /opt/s3-tests
ENV PYTHONDONTWRITEBYTECODE=1
ENTRYPOINT ["/usr/local/bin/run-subset"]
