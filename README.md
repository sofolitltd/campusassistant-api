# Campus Assistant API

Enterprise-grade Go backend for campus resource management with **JWT authentication**.

## 🚀 Getting Started

### Prerequisites
- Go 1.25.7+
- PostgreSQL

### Installation
1. Clone the repository
2. Set up your `.env` file (see `.env` for required variables)
3. Install dependencies:
   ```bash
   go mod tidy
   ```

### Running the server
```bash
make run
```
This will automatically generate Swagger documentation and start the server on `http://localhost:8080`.

## 🔐 Authentication

This API uses **JWT (JSON Web Token) + Bcrypt** for secure authentication.

### Quick Start

**Register a new user:**
```bash
POST /api/v1/auth/register
{
  "email": "user@example.com",
  "password": "SecurePass123!",
  "first_name": "John",
  "last_name": "Doe"
}
```

**Login:**
```bash
POST /api/v1/auth/login
{
  "email": "user@example.com",
  "password": "SecurePass123!"
}
```

**Access protected routes:**
```bash
GET /api/v1/auth/me
Authorization: Bearer <your_access_token>
```

📖 **[Complete Authentication Guide](JWT_AUTH.md)**

## 📚 API Documentation

Once the server is running, you can access the interactive Swagger UI at:
👉 **[http://localhost:8080/swagger/index.html](http://localhost:8080/swagger/index.html)**

## 🛠 Features

- **🔐 JWT Authentication**: Secure token-based auth with bcrypt password hashing
- **👥 Role-Based Access Control**: Super admin, university admin, teacher, student roles
- **🏛 Clean Architecture**: Decoupled layers for scalability
- **📦 Unified Resources**: Handle Notes, Books, and Questions via a single model
- **🎓 Academic Hierarchy**: Manage Universities, Departments, Semesters, and Batches
- **☁️ Cloud Integration**: R2 Storage for file uploads
- **🔒 Enterprise Ready**: Soft deletions, Audit logs, and JSONB support

## 🧪 Testing

### Authentication Tests
Use the `auth_tests.rest` file for JWT authentication testing:
```bash
# Test registration, login, token refresh, and protected routes
```

### API Tests
Use the `api_tests.rest` file with the VS Code REST Client for automated integration testing.

## 📁 Project Structure

```
campusassistant-api/
├── cmd/api/              # Application entry point
├── internal/
│   ├── config/           # Configuration management
│   ├── delivery/http/    # HTTP handlers and routes
│   │   ├── handler/      # Request handlers (including auth)
│   │   └── middleware/   # JWT, API key, RBAC middleware
│   ├── domain/           # Business entities
│   ├── repository/       # Data access layer
│   └── usecase/          # Business logic
├── pkg/
│   ├── auth/             # JWT and password utilities
│   └── storage/          # File storage (R2)
├── auth_tests.rest       # Authentication API tests
├── api_tests.rest        # General API tests
└── JWT_AUTH.md           # Authentication documentation
```

## 🔧 Configuration

Key environment variables in `.env`:

```bash
# Database
DATABASE_URL=postgresql://user:pass@localhost:5432/campusassistant

# JWT Authentication
JWT_SECRET=your-super-secret-key-min-32-chars
JWT_ACCESS_TOKEN_EXPIRY=15    # minutes
JWT_REFRESH_TOKEN_EXPIRY=168  # hours (7 days)

# API Security
API_KEY=your-api-key

# Cloudflare R2 (optional)
R2_ACCESS_KEY_ID=...
R2_SECRET_ACCESS_KEY=...
```

## 📖 Documentation

- **[JWT Authentication Guide](JWT_AUTH.md)** - Complete auth documentation
- **[Implementation Summary](AUTH_IMPLEMENTATION_SUMMARY.md)** - What was built
- **[Project Plan](PLAN.md)** - Architecture overview

## 🌟 Why This Stack?

| Component | Choice | Benefit |
|-----------|--------|---------|
| **Go** | High performance | Fast, concurrent, type-safe |
| **PostgreSQL** | Relational DB | ACID compliance, complex queries |
| **JWT** | Token auth | Stateless, scalable, standard |
| **Bcrypt** | Password hashing | Industry standard, secure |
| **Gin** | Web framework | Fast routing, middleware support |
| **GORM** | ORM | Rapid development, migrations |

## 🚦 API Endpoints

### Authentication (Public)
- `POST /api/v1/auth/register` - Create account
- `POST /api/v1/auth/login` - Login
- `POST /api/v1/auth/refresh` - Refresh token
- `GET /api/v1/auth/me` - Get current user (JWT required)

### Resources (API Key Required)
- Universities, Departments, Sessions, Batches
- Students, Teachers, Staff
- Resources (Notes, Books, Questions)
- Halls, Transport, Semesters

## 🤝 Contributing

This is an enterprise-grade project following clean architecture principles. Contributions should maintain:
- Clean separation of concerns
- Comprehensive error handling
- Security best practices
- Proper documentation

