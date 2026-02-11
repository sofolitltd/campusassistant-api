# Project Plan: Campus Assistant Backend (Enterprise Go/Gin)

## 1. Architecture Overview: Clean Architecture
We will follow the **Standard Go Project Layout** combined with **Clean Architecture** principles to ensure scalability, maintainability, and testability.

### Layers:
1.  **Domain Layer (`internal/domain`)**: Core business logic, entities, and repository interfaces. Does *not* depend on anything external (DB, HTTP, etc.).
2.  **Usecase Layer (`internal/usecase`)**: Orchestrates the flow of data between the UI/API (Delivery) and Data (Repository). Contains application-specific business rules.
3.  **Repository Layer (`internal/repository`)**: Implements the interfaces defined in the Domain layer for data access (PosterSQL via GORM).
4.  **Delivery Layer (`internal/delivery`)**: HTTP handlers using **Gin**. Dealing with request/response, validation, and calling Usecases.

### Why this structure?
-   **Decoupled**: Switching from HTTP to gRPC or CLI is easy by swapping the Delivery layer.
-   **Testable**: Unit tests can mock Repositories and test Usecases in isolation.
-   **Clean**: Clear separation of concerns.

## 2. Technology Stack & Justification

| Component | Choice | Justification |
| :--- | :--- | :--- |
| **Language** | Go (Golang) | High performance, concurrency, strong typing. |
| **Web Framework** | **Gin** | Request routing, middleware support, high performance, large ecosystem. |
| **Database** | **PostgreSQL (Neon)** | Robust relational database, ACID compliance, JSONB support if needed. |
| **ORM / Data Access** | **GORM** | Rapid development, active record pattern, migration support. Ideal for complex entity relationships (University -> Dept -> Student). For strict performance later, we can optimize raw queries if needed. |
| **Configuration** | **Viper** | Handles environment variables, config files, and defaults seamlessly. |
| **Logging** | **Zap** (Uber) | Extremely fast, structured logging (JSON) essential for enterprise observability. |
| **Authentication** | **JWT** (golang-jwt) | Stateless authentication standard for REST APIs. |
| **Documentation** | **Swagger** (Swaggo) | Auto-generated API documentation. |

## 3. Database Schema Strategy (High Level)

### Core Hierarchy
*   **University**: The root entity.
*   **Department**: Belongs to a University.
*   **Session**: Academic session (e.g., 2023-2024). Can be global or per University.
*   **Batch**: Specific group of students (e.g., "CSE 2023"), belongs to Department + Session.

### Users & Roles
*   **User**: Base authentication entity (Email, Password, Role).
    *   *Roles*: `super_admin`, `admin` (University/Dept level), `teacher`, `student`, `staff`.
*   **Profile Tables**:
    *   `Student`: Links to `User`, `Batch`, `Department`.
    *   `Teacher`: Links to `User`, `Department`.
    *   `Staff`: Links to `User`, `Department`.

### Academic Resources (The "Filters")
*   **Book/Question/Note/Syllabus**:
    *   All should link to at least `DepartmentID` and `UniversityID` (for faster filtering).
    *   Likely also `Subject` or `Course` (tbd).

## 4. Implementation Roadmap

1.  **Project Setup**: Initialize directory structure, `go.mod`, tools.
2.  **Configuration & Database**: Setup Viper and GORM connection to Neon.
3.  **Domain Modeling**: Create structs for `University`, `Department`, `User`.
4.  **Repository Implementation**: Generic repository implementation (Create, Find, Update, Delete).
5.  **Usecase Implementation**: Business logic for creating Unversities/Departments.
6.  **HTTP Handlers**: Gin handlers for the endpoints.
7.  **Authentication**: Middleware for JWT and Role-Based Access Control (RBAC).

## 5. Areas for Improvement / Future Tech
1.  **Caching**: Redis for `GET /universities` and `GET /departments` as they rarely change but are frequently read.
2.  **Search**: Elasticsearch or Postgres Full-Text Search for `Notes`, `Questions`, `Books`.
3.  **File Storage**: AWS S3 or MinIO for storing PDF notes/syllabuses. Storing binary in DB is bad practice.
4.  **Real-time**: WebSockets for notifications (e.g., "New Note Uploaded").

Let's start building.
