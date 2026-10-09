import { defineComponent, h, ref } from "vue";

import MaskedUrlInput from "./MaskedUrlInput.vue";

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
        h("strong", "Review the upload target"),
        ". The saved record is rendered and the document sent there; unsaved edits are never sent.",
      ]),
      // "Save first" is an instruction, not a failure. It rendered in the
      // `alert` chrome, which is the error styling, so a neutral precondition
      // arrived looking like something had gone wrong.
      !props.saved
        ? h("p", { class: "row-popover-note", role: "status" }, "Save this record before uploading.")
        : null,
      props.error ? h("p", { class: "row-popover-error", role: "alert" }, props.error) : null,
      h("div", { class: "form-grid" }, [
        // A destination can carry a credential in its path or query, as a
        // provider link does, so it reads masked after the host and shows
        // whole only while it is edited or revealed (design 28, Security).
        h("div", { class: "field" }, [
          h("span", { class: "field-label" }, "Destination"),
          h(MaskedUrlInput, {
            modelValue: destination.value,
            ariaLabel: "Destination",
            disabled: disabled(),
            placeholder: "Where the recomposed definition is sent",
            "onUpdate:modelValue": (value: string) => { destination.value = value; },
          }),
        ]),
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
        }, props.busy ? "Uploading…" : "Upload document"),
      ]),
    ]);
  },
});
