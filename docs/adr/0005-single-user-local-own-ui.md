# ADR 0005 — Un usuario, local, UI web propia; Engram Cloud se apaga

Estado: aceptado · Origen: obs 6570

## Contexto

Engram Cloud (compose con Postgres en :18080) solo interesaba por la UI web,
que además el usuario considera fea. El sync multiusuario dio dos sesiones de
bugs. Los datos son 100% locales.

## Decisión

Sin sync propio, sin multiusuario. El daemon sirve una UI web en localhost
sobre su SQLite más el ATTACH de Engram. El stack cloud deja de ser necesario y
se apaga. No se toca ni mejora el Cloud Sync de upstream.

Las tablas propias se diseñan para que una lectura compartida futura sea un
paso y no una reescritura: ids estables (ULID), `updated_at` en todo, sin FKs
a rowid.

## Consecuencias

- Cae el costo más alto del diseño anterior.
- Si alguien más adopta el flujo, el sync se diseña entonces, con datos reales.
