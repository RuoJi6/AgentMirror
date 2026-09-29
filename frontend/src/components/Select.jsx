import React, { useRef, useState } from "react";
import * as Primitive from "@radix-ui/react-select";
import { Check, ChevronDown, ChevronUp } from "lucide-react";

// Keep existing option values (including the empty "all/unbound" value) and
// change handlers while sharing one accessible menu across the administration UI.
const encode = (value) => `option:${String(value)}`;
const textOf = (children) =>
  React.Children.toArray(children)
    .map((child) =>
      React.isValidElement(child) ? textOf(child.props.children) : child,
    )
    .join("");

export default function Select({
  children,
  value,
  onChange,
  disabled,
  name,
  required,
  className = "",
  placeholder = "请选择",
  ...props
}) {
  const trigger = useRef(null);
  const [open, setOpen] = useState(false);
  const [container, setContainer] = useState(null);
  const options = React.Children.toArray(children)
    .filter(React.isValidElement)
    .map(({ props: option }) => ({
      value: String(option.value ?? textOf(option.children)),
      label: option.children,
      text: textOf(option.children),
      disabled: option.disabled,
    }));
  const selected = options.find((option) => option.value === String(value));

  return (
    <Primitive.Root
      value={selected ? encode(selected.value) : ""}
      onValueChange={(next) =>
        onChange?.({ target: { value: next.slice("option:".length), name } })
      }
      open={open}
      onOpenChange={(next) => {
        // Keep menus inside the active top layer, including fullscreen boards.
        if (next) {
          const fullscreen = document.fullscreenElement;
          const dialog = trigger.current?.closest("dialog");
          setContainer(
            fullscreen?.contains(trigger.current)
              ? dialog && fullscreen.contains(dialog)
                ? dialog
                : fullscreen
              : dialog || document.body,
          );
        }
        setOpen(next);
      }}
      disabled={disabled || options.length === 0}
      required={required}
    >
      <Primitive.Trigger
        {...props}
        ref={trigger}
        className={`select-trigger ${className}`}
        data-value={selected?.value ?? ""}
      >
        <span className="select-value">
          <Primitive.Value placeholder={placeholder} />
        </span>
        <Primitive.Icon className="select-chevron">
          <ChevronDown size={16} aria-hidden="true" />
        </Primitive.Icon>
      </Primitive.Trigger>
      <Primitive.Portal container={container}>
        <Primitive.Content
          className="select-content"
          position="popper"
          sideOffset={5}
          align="start"
          collisionPadding={12}
          onEscapeKeyDown={(event) => event.stopPropagation()}
        >
          <Primitive.ScrollUpButton className="select-scroll-button">
            <ChevronUp size={15} />
          </Primitive.ScrollUpButton>
          <Primitive.Viewport className="select-viewport">
            {options.map((option) => (
              <Primitive.Item
                key={option.value}
                value={encode(option.value)}
                data-select-value={option.value}
                textValue={option.text}
                disabled={option.disabled}
                className="select-option"
              >
                <Primitive.ItemText>{option.label}</Primitive.ItemText>
                <Primitive.ItemIndicator className="select-check">
                  <Check size={15} aria-hidden="true" />
                </Primitive.ItemIndicator>
              </Primitive.Item>
            ))}
          </Primitive.Viewport>
          <Primitive.ScrollDownButton className="select-scroll-button">
            <ChevronDown size={15} />
          </Primitive.ScrollDownButton>
        </Primitive.Content>
      </Primitive.Portal>
    </Primitive.Root>
  );
}
