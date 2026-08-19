import { defineComponent, h, ref } from "vue";

function field(label: string, control: ReturnType<typeof h>) {
  return h("label", { class: "field" }, [h("span", { class: "field-label" }, label), control]);
}

export default defineComponent({
  name: "SubscriptionPublishControl",
  props: {
    saved: { type: Boolean, required: true },
    readOnly: { type: Boolean, required: true },
    busy: { type: Boolean, required: true },
    error: { type: String, default: "" },
  },
  emits: ["publish"],
  setup(props, { emit }) {
    const destination = ref("");
    const method = ref("PUT");
    const format = ref("plain");
    const disabled = () => props.readOnly || !props.saved;
    return () => h("form", {
      class: "publish-review",
      onSubmit: (event: Event) => {
        event.preventDefault();
        emit("publish", destination.value, method.value, format.value);
      },
    }, [
      h("p", { class: "row-popover-copy" }, [
        h("strong", "Review publish target"),
        ". Publishing recomposes the saved definition; unsaved edits are never sent.",
      ]),
      // "Save first" is an instruction, not a failure. It rendered in the
      // `alert` chrome, which is the error styling, so a neutral precondition
      // arrived looking like something had gone wrong.
      !props.saved
        ? h("p", { class: "row-popover-note", role: "status" }, "Save this definition before publishing.")
        : null,
      props.error ? h("p", { class: "row-popover-error", role: "alert" }, props.error) : null,
      h("div", { class: "form-grid" }, [
        field("Destination", h("input", {
          value: destination.value, type: "url", required: true, autocomplete: "off", disabled: disabled(),
          placeholder: "https://…",
          onInput: (event: Event) => { destination.value = (event.target as HTMLInputElement).value; },
        })),
        field("Method", h("select", {
          class: "select",
          value: method.value, disabled: disabled(), onChange: (event: Event) => { method.value = (event.target as HTMLSelectElement).value; },
        }, ["PUT", "POST", "PATCH"].map((value) => h("option", { value }, value)))),
        field("Format", h("select", {
          class: "select",
          value: format.value, disabled: disabled(), onChange: (event: Event) => { format.value = (event.target as HTMLSelectElement).value; },
        }, [h("option", { value: "plain" }, "Plain"), h("option", { value: "base64" }, "Base64"), h("option", { value: "sing-box" }, "sing-box")])),
      ]),
      h("div", { class: "form-actions" }, [
        h("button", {
          class: "button button-primary",
          type: "submit",
          disabled: disabled() || !destination.value.trim() || props.busy,
        }, props.busy ? "Publishing…" : "Publish saved definition"),
      ]),
    ]);
  },
});
