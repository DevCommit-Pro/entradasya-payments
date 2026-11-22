# Payments — Go

Simulador interno del contrato API §§9 y 14.2. Sin dinero real ni DB. Usa únicamente biblioteca estándar Go. Solo transacciones seguras en memoria; reiniciar borra resultados y garantías de idempotencia previas.

## Ejecutar

Usa únicamente biblioteca estándar Go; no requiere dependencias externas.

```sh
cp .env.example .env
# Editar INTERNAL_API_KEY para usar la misma clave que los servicios backend.
set -a
source .env
set +a
go run ./cmd/api
```

Variables: PORT (4004 por defecto, 1–65535), INTERNAL_API_KEY (obligatoria, mínimo 32 bytes). Escucha en 127.0.0.1. No necesita JWT_SECRET: no expone operaciones públicas de usuarios. El proceso usa timeouts HTTP, cierre por señal y logs JSON de consola sin cuerpos, tarjetas o claves.

## Endpoints

- GET /health → 200 `{status,service,storage:"memory",time}`.
- POST /internal/payments/charge → 200 transacción approved o declined. Requiere X-Internal-Key.
- GET /internal/payments/by-order/{orderId} → 200 transacción conocida o 404. Requiere X-Internal-Key; consulta sin efectos para recuperación de orders.

Body de charge:

```json
{"orderId":"o-demo","amount":170.50,"currency":"BOB","card":{"number":"4242424242424242","holder":"Camila Rojas","expiry":"08/28","cvc":"123"}}
```

Respuesta:

```json
{"transactionId":"pay-...","orderId":"o-demo","status":"approved","amount":170.50,"currency":"BOB","cardLast4":"4242","createdAt":"2030-01-01T12:00:00Z"}
```

Solo tarjetas **ficticias**. Terminación 0000 → declined, cualquier otra → approved, siempre que el request sea válido. Validación de formato, no Luhn, vencimiento bancario ni autorización real: número de 13–19 dígitos, CVC de 3–4, titular no vacío y expiry MM/AA con mes 01–12.

Importes como número JSON en Bs, calculados en centavos exactos. Cero permitido; negativos, fracciones de centavo, null/string o valores fuera de `2^53−1` centavos se rechazan. No se redondean silenciosamente decimales inválidos.

## Idempotencia y seguridad

Una orden válida produce una transacción. Repetición equivalente devuelve exactamente el mismo resultado (incluso declined). Cambiar importe o datos relevantes de tarjeta para ese orderId devuelve 409 idempotency_conflict; otra tarjeta tras rechazo requiere otra orden, no mutar la anterior.

Registro y comprobación se hacen bajo mutex. Solo se guardan importe, moneda, estado, fecha, orderId, transactionId, últimos cuatro dígitos y un fingerprint HMAC con clave aleatoria del proceso. No se guarda PAN/CVC ni hash sin clave de los datos de tarjeta. cardLast4 usa strings.Clone: un substring Go normal podría retener el backing string con el PAN completo. Los structs de entrada se copian antes de normalizar, evitando modificar inputs compartidos concurrentemente.

Los datos sensibles existen transitoriamente al procesar el request; no se promete borrado seguro de toda la memoria administrada de Go. Nunca se vuelcan a logs.

Errores `{error:{code,message}}`: 400 invalid_request, 401 unauthorized, 404 not_found, 405 invalid_request con Allow, 409 idempotency_conflict, 500 internal_error. Request máximo 64 KiB; JSON desconocido/malformado o cuerpos múltiples se rechazan.

**Recuperación:** 404 por orderId solo significa que no hay resultado conocido en ese instante. No prueba que una solicitud anterior incierta no vaya a completarse; orders no debe liberar cupos ni iniciar otro cobro por ese 404 aislado.

## Pruebas

```sh
go test -race ./...
go vet ./...
```

Nueve pruebas de nivel superior con escenarios de validación, precisión monetaria, idempotencia/concurrencia, privacidad de datos y logs. Usan httptest.ResponseRecorder sin puerto; no sustituyen integración de red ni orders. No se implementan reembolsos ni pagos reales.
