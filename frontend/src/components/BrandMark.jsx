import React from "react";
import mark from "../assets/agentmirror-mark.svg";

// Shared with the favicon so the approved mark stays identical at every size.
export default function BrandMark({ size = 29 }) {
  return (
    <img
      className="brand-mark"
      src={mark}
      width={size}
      height={size}
      alt=""
      aria-hidden="true"
      draggable={false}
      style={{ display: "block", flexShrink: 0 }}
    />
  );
}
