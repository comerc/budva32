build-image:
	docker build -fDockerfile -ttelegram-forwarder .

up:
	docker-compose -fdocker-compose.yml up