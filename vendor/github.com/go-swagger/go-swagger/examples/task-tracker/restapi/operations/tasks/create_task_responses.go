package tasks

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"net/http"

	"github.com/go-swagger/go-swagger/httpkit"
)

/*CreateTaskCreated Task created

swagger:response createTaskCreated
*/
type CreateTaskCreated struct {
}

// NewCreateTaskCreated creates CreateTaskCreated with default headers values
func NewCreateTaskCreated() *CreateTaskCreated {
	return &CreateTaskCreated{}
}

// WriteResponse to the client
func (o *CreateTaskCreated) WriteResponse(rw http.ResponseWriter, producer httpkit.Producer) {

	rw.WriteHeader(201)
}

/*CreateTaskDefault create task default

swagger:response createTaskDefault
*/
type CreateTaskDefault struct {
	_statusCode int
}

// NewCreateTaskDefault creates CreateTaskDefault with default headers values
func NewCreateTaskDefault(code int) *CreateTaskDefault {
	if code <= 0 {
		code = 500
	}

	return &CreateTaskDefault{
		_statusCode: code,
	}
}

// WithStatusCode adds the status to the create task default response
func (o *CreateTaskDefault) WithStatusCode(code int) *CreateTaskDefault {
	o._statusCode = code
	return o
}

// WriteResponse to the client
func (o *CreateTaskDefault) WriteResponse(rw http.ResponseWriter, producer httpkit.Producer) {

	rw.WriteHeader(o._statusCode)
}
