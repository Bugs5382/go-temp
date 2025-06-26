## 📡 About

This is my first Go-based project, designed to collect temperature readings from a physical sensor (e.g., DS18B20) connected to a Raspberry Pi. The app runs inside a Kubernetes cluster and operates as one of several microservices in a broader IoT system.

###  Key Features:

* **Sensor Data Acquisition**: Reads temperature data directly from a connected hardware sensor.
* **Dual RabbitMQ Publishing**:

    * Sends data to a **local RabbitMQ** instance (within the same Kubernetes cluster).
    * Forwards the same data to a **centralized RabbitMQ** service used for global aggregation, monitoring, and alerting.
* **Microservice Architecture**: This app is part of a larger microservices ecosystem that powers a **React-based frontend** with **GraphQL** for data access and presentation.
* **Designed for Edge Computing**: Runs on Kubernetes-managed Raspberry Pi nodes to allow distributed deployments and real-time telemetry.
