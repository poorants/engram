# The service image. Neither Python nor a virtualenv is installed on the host —
# both live only inside the container.
FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1

WORKDIR /srv/app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Code only. No documents are in the image or in a volume — bodies arrive over
# HTTP and live in Postgres.
COPY sql/ ./sql/
COPY app/ ./app/

RUN useradd -r -u 10001 engram
USER engram

EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD python -c "import urllib.request,sys; sys.exit(0 if __import__('json').load(urllib.request.urlopen('http://127.0.0.1:8081/healthz',timeout=4))['ok'] else 1)"

CMD ["uvicorn", "app.web:app", "--host", "0.0.0.0", "--port", "8081", \
     "--proxy-headers", "--no-server-header"]
