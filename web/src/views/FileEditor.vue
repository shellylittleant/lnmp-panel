<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from "vue";
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { html } from "@codemirror/lang-html";
import { javascript } from "@codemirror/lang-javascript";
import { css } from "@codemirror/lang-css";
import { json } from "@codemirror/lang-json";
import { php } from "@codemirror/lang-php";

const props = defineProps<{ modelValue: string; filename: string }>();
const emit = defineEmits<{ "update:modelValue": [string] }>();
const host = ref<HTMLDivElement | null>(null);
let view: EditorView | undefined;

function langExt(name: string) {
  const n = name.toLowerCase();
  if (n.endsWith(".php")) return php();
  if (n.endsWith(".css")) return css();
  if (n.endsWith(".json")) return json();
  if (n.endsWith(".js") || n.endsWith(".ts")) return javascript();
  if (n.endsWith(".html") || n.endsWith(".htm") || n.endsWith(".vue") || n.endsWith(".xml")) return html();
  return javascript();
}

onMounted(() => {
  if (!host.value) return;
  view = new EditorView({
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        basicSetup,
        langExt(props.filename),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) emit("update:modelValue", u.state.doc.toString());
        }),
      ],
    }),
    parent: host.value,
  });
});

watch(
  () => props.modelValue,
  (v) => {
    if (!view) return;
    if (v !== view.state.doc.toString()) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
    }
  }
);

onUnmounted(() => view?.destroy());
</script>

<template>
  <div ref="host" class="cm-host"></div>
</template>

<style scoped>
.cm-host {
  border: 1px solid #eee;
  min-height: 320px;
  text-align: left;
}
</style>
