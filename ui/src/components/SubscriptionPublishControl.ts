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
      h("p", [h("strong", "Review publish target"), " — publishing recomposes the saved definition; unsaved edits are never sent."]),
      !props.saved ? h("p", { class: "alert", role: "status" }, "Save this definition before publishing.") : null,
      props.error ? h("p", { class: "alert", role: "alert" }, props.error) : null,
      field("Destination", h("input", {
        value: destination.value, type: "url", required: true, autocomplete: "off", disabled: disabled(),
        onInput: (event: Event) => { destination.value = (event.target as HTMLInputElement).value; },
      })),
      field("Method", h("select", {
        value: method.value, disabled: disabled(), onChange: (event: Event) => { method.value = (event.target as HTMLSelectElement).value; },
      }, ["PUT", "POST", "PATCH"].map((value) => h("option", { value }, value)))),
      field("Format", h("select", {
        value: format.value, disabled: disabled(), onChange: (event: Event) => { format.value = (event.target as HTMLSelectElement).value; },
      }, [h("option", { value: "plain" }, "Plain"), h("option", { value: "base64" }, "Base64"), h("option", { value: "sing-box" }, "sing-box")])),
      h("button", { class: "button button-primary", type: "submit", disabled: disabled() || !destination.value.trim() || props.busy }, "Publish saved definition"),
    ]);
  },
});
