FROM --platform=linux/amd64 ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive

RUN apt update
RUN apt install -y ca-certificates
RUN apt install -y git
RUN apt install -y wget
RUN apt install -y cmake
RUN apt install -y ninja-build
RUN apt install -y pkg-config
RUN apt install -y python3
RUN apt install -y python3-pip
RUN apt-get install -y flex bison

RUN wget https://go.dev/dl/go1.21.5.linux-amd64.tar.gz -O /tmp/go1.21.5.linux-amd64.tar.gz && \
    rm -rf /usr/local/go && \
    tar -C /usr/local -xzf /tmp/go1.21.5.linux-amd64.tar.gz && \
    ln -s /usr/local/go/bin/go /usr/bin/go
ENV PATH="/usr/local/go/bin:${PATH}"


# UPMEM SDK
WORKDIR /root
COPY upmem-2023.2.0-Linux-x86_64.tar.gz /root/
RUN tar -zxvf upmem-2023.2.0-Linux-x86_64.tar.gz
RUN echo "source /root/upmem-2023.2.0-Linux-x86_64/upmem_env.sh" > /root/.bashrc

WORKDIR /root/uPIMulator
