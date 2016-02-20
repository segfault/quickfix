package collinqqualgrp

//NoCollInquiryQualifier is a repeating group in CollInqQualGrp
type NoCollInquiryQualifier struct {
	//CollInquiryQualifier is a non-required field for NoCollInquiryQualifier.
	CollInquiryQualifier *int `fix:"896"`
}

//Component is a fix50sp1 CollInqQualGrp Component
type Component struct {
	//NoCollInquiryQualifier is a non-required field for CollInqQualGrp.
	NoCollInquiryQualifier []NoCollInquiryQualifier `fix:"938,omitempty"`
}

func New() *Component { return new(Component) }