# ADR 0012 — Jerarquía de tareas con `parent_task_id`

Estado: aceptado · Origen: pedido directo del usuario (project `neugram`)

## Contexto

Una tarea grande se divide a veces en partes que se pinean y trackean por
separado, pero que siguen siendo "la misma tarea" a efectos de reporte. El
pedido fue explícito: resolverlo con el mínimo de estructura posible, sin
closure table, materialized path ni tabla de relación aparte.

Dos niveles no alcanzan. El trabajo real se agrupa en tres escalones —un
épico que dura semanas, las tareas en que se parte, y las sub-tareas con que
se reparte cada una entre sesiones o agentes— y con el tope en 2 el escalón
del medio no tiene dónde ir: o el épico se convierte en una tarea más y se
pierde la agrupación, o las sub-tareas cuelgan directo del épico y la tarea
deja de existir como unidad.

## Decisión

`tasks.parent_task_id` auto-referencial, `NULL` = épico. Con eso:

- **Profundidad fija en 3 niveles**: épico (nivel 1), tarea (nivel 2),
  sub-tarea (nivel 3). Es un tope duro, no una jerarquía general. Son niveles,
  no tipos: una sola tabla, las mismas columnas y las mismas reglas de pin,
  status y tracker en los tres.
- **Dos triggers, sin recursión**: `tasks_parent_depth_ins`/`_upd` verifican,
  al fijar el padre `P` de un nodo `X`, que `P` esté en el nivel 1 o 2 —dos
  lookups, el padre y el abuelo de `P`— y que el subárbol de `X` quepa por
  debajo: con `P` en el nivel 2, `X` no puede tener hijas; con `P` en el nivel
  1, no puede tener nietos. La altura de `X` está acotada por el mismo tope,
  así que mirar hijas y nietos la agota. El `CHECK (parent_task_id != id)`
  solo cubre la auto-referencia; SQLite no admite subqueries en `CHECK`.
- **Sin ciclos, por el chequeo de nivel**: un ciclo no tiene raíz, así que
  ningún nodo del ciclo llega a un `parent_task_id IS NULL` subiendo. El
  chequeo de nivel sube dos pasos buscando ese `NULL` y aborta si no lo
  encuentra. Con el tope en 3, el arco que cerraría un ciclo apunta siempre a
  la propia tarea, a una hija o a una nieta, y los tres casos caen en alguno
  de los `RAISE`. El caso analizado y la batería que lo prueba están en
  `specs/data-model.md`.
- **Producto no se hereda por constraint**: `task add --parent` copia el
  `product_id` del padre como default en la capa CLI, sin volver a
  sincronizarlo. Evita un trigger más para un caso que la UI puede mostrar
  desalineado sin que rompa nada.
- **Status no se deriva**: sigue la regla general de `domain.md` — lo declara
  el usuario, ningún hook lo toca. Cerrar un épico con tareas abiertas es
  válido; el CLI solo avisa, contando hijas directas.
- **La búsqueda sube el árbol, y no baja salvo que se lo pidan**: una sesión
  pinea cualquier `tasks.id`, de cualquier nivel. `mem_search`/`mem_context`
  traen por default las observaciones de la tarea y de todos sus ancestros
  (`include_ancestors`, con tres niveles son a lo sumo dos ids extra), porque
  el contexto del épico es justo lo que le falta a una sesión pineada a una
  sub-tarea. Bajar es opt-in (`include_subtasks`) y `task show` lo hace
  siempre: el subárbol entero, con un `IN` anidado de dos vueltas.
- **Borrado del padre**: `ON DELETE RESTRICT`. Hay que reasignar (`task mv`)
  o borrar las hijas antes, de abajo hacia arriba.

## Consecuencias

- Cero tablas nuevas: un campo, un índice y dos triggers.
- La profundidad 3 es una limitación deliberada. Un cuarto nivel no es una
  excepción a este ADR: es cambiar la forma de los triggers otra vez, y a esa
  altura ya conviene discutir closure table contra el costo de mantenerla.
- `task mv --parent` es la operación cara del modelo: mover un nodo arrastra
  su subárbol y puede desbordar el tope. Falla con un mensaje que nombra la
  regla violada; no hay reacomodo automático.
- En las tools MCP (`specs/api.md`) la jerarquía entra por `task_pin`
  (`parent`), `task_list` (`parent`, `roots_only`, `tree`), `task_show`
  (`include_subtasks`) y, ahora, `mem_search`/`mem_context`
  (`include_ancestors`, `include_subtasks`). El flag que sube se llama por los
  ancestros y no por el padre porque con tres niveles trae a los dos.
- El tablero de la UI (`/`) y la ficha de tarea (`/tasks/{key}`) muestran el
  árbol de tres niveles y el breadcrumb de ancestros; sin eso la jerarquía es
  invisible fuera del CLI.
